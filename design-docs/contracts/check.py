#!/usr/bin/env python3
"""Проверка контрактов design-docs.

  python3 design-docs/contracts/check.py            # JSON/YAML/схемы/примеры
  DATABASE_URL=postgres://... python3 .../check.py   # + DDL и SQL-инварианты в пустой тестовой БД

Зависимости: pip install pyyaml jsonschema
"""
from __future__ import annotations

import json
import copy
import os
import subprocess
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
failures: list[str] = []


def ok(msg: str) -> None:
    print(f"  ok   {msg}")


def fail(msg: str) -> None:
    failures.append(msg)
    print(f"  FAIL {msg}")


def load_json(name: str):
    try:
        return json.loads((HERE / name).read_text(encoding="utf-8"))
    except Exception as e:  # noqa: BLE001
        fail(f"{name}: {e}")
        return None


def main() -> int:
    print("JSON")
    schemas = {n: load_json(n) for n in ["scenario.schema.json", "scenario-file.schema.json", "evidence.schema.json", "evidence.operator112.schema.json", "assessment-inputs.schema.json", "rubric.schema.json", "sse-events.schema.json", "tasks.schema.json", "voice-assets-manifest.schema.json"]}
    examples = {n: load_json(n) for n in ["scenario.example.json", "evidence.example.json", "rubric.default.json", "assessment-inputs.example.json"]}
    for n, v in {**schemas, **examples}.items():
        if v is not None:
            ok(n)

    print("OpenAPI")
    try:
        import yaml  # type: ignore
        spec = yaml.safe_load((HERE / "openapi.yaml").read_text(encoding="utf-8"))
        assert spec["openapi"].startswith("3.1"), "openapi version"
        paths = spec["paths"]
        comps = spec["components"]["schemas"]
        # все $ref разрешаются
        refs = set()

        def walk(node):
            if isinstance(node, dict):
                if "$ref" in node:
                    refs.add(node["$ref"])
                for v in node.values():
                    walk(v)
            elif isinstance(node, list):
                for v in node:
                    walk(v)

        walk(spec)
        for r in sorted(refs):
            kind, name = r.split("/")[-2], r.split("/")[-1]
            if name not in spec["components"].get(kind, {}):
                fail(f"unresolved $ref {r}")
        ok(f"{len(paths)} paths, {len(comps)} schemas, {len(refs)} refs resolved")
        # каждая операция имеет tag и responses
        for p, item in paths.items():
            for m, op in item.items():
                if m in ("get", "post", "put", "patch", "delete"):
                    if not op.get("tags") or not op.get("responses"):
                        fail(f"{m.upper()} {p}: missing tags/responses")
    except ImportError:
        fail("pyyaml not installed; OpenAPI was not checked")
    except Exception as e:  # noqa: BLE001
        fail(f"openapi.yaml: {e}")

    print("JSON Schema validation")
    try:
        import jsonschema  # type: ignore
        from jsonschema import Draft202012Validator as V

        for n, s in schemas.items():
            if s is not None:
                V.check_schema(s)
                ok(f"{n} is a valid draft 2020-12 schema")
        pairs = [("scenario.schema.json", "scenario.example.json"), ("evidence.schema.json", "evidence.example.json"), ("rubric.schema.json", "rubric.default.json"), ("assessment-inputs.schema.json", "assessment-inputs.example.json")]
        for sn, en in pairs:
            if schemas[sn] is None or examples[en] is None:
                continue
            errs = sorted(V(schemas[sn], format_checker=V.FORMAT_CHECKER).iter_errors(examples[en]), key=lambda e: str(list(e.path)))
            if errs:
                for e in errs[:10]:
                    fail(f"{en}: {'/'.join(map(str, e.path))}: {e.message[:160]}")
            else:
                ok(f"{en} validates against {sn}")
        voice_manifest = {
            "schema": "emsim/voice-assets-manifest/v1",
            "assets": [{"scenario_key": "pilot-phone", "version": 1, "contact_key": "crew_leader", "phrase": "greeting", "file": "crew-greeting.wav", "sha256": "a" * 64, "size": 16, "mime": "audio/wav"}],
        }
        if not V(schemas["voice-assets-manifest.schema.json"]).is_valid(voice_manifest):
            fail("positive voice-assets manifest is invalid")
        else:
            bad_manifest = copy.deepcopy(voice_manifest)
            bad_manifest["assets"][0]["phrase"] = "other"
            if V(schemas["voice-assets-manifest.schema.json"]).is_valid(bad_manifest):
                fail("negative voice-assets manifest unexpectedly validates")
            else:
                ok("voice-assets manifest positive/negative examples validate")
        # SSE: несколько инстансов
        sse = schemas["sse-events.schema.json"]
        sse_examples = [
            {"event": "stream.ready", "id": "1:0", "data": {"cursor": "1:0"}},
            {"event": "invalidate", "id": "1:1", "data": {"lesson_id": "019230a3-0000-7c0a-9a1f-000000000001", "item_id": "019230a4-6b1e-7c0a-9a1f-3f2a1b2c3d4e"}},
            {"event": "invalidate", "id": "1:2", "data": {"user_id": "0192309f-0000-7c0a-9a1f-000000000002"}},
            {"event": "resync", "id": "2:0", "data": {"cursor": "2:0"}},
        ]
        if sse:
            v = V(sse)
            bad = [ex["event"] for ex in sse_examples if not v.is_valid(ex)]
            if bad:
                fail(f"sse examples invalid: {bad}")
            else:
                ok(f"{len(sse_examples)} SSE examples validate")
        tasks = schemas["tasks.schema.json"]
        task_examples = [
            {"kind": "assessment.evaluate", "scope_type": "item", "scope_id": "019230a4-6b1e-7c0a-9a1f-3f2a1b2c3d4e", "payload": {"item_id": "019230a4-6b1e-7c0a-9a1f-3f2a1b2c3d4e", "evidence_digest": "9f2c1b5e0a7d4c3b8e6f1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d", "rubric_version": "dds/rubric-v1"}},
            {"kind": "scenario.generate", "scope_type": "scenario", "scope_id": None, "payload": {"service": "dds_district", "difficulty": 4, "requested_by": "0192309f-0000-7c0a-9a1f-000000000002", "prompt_version": "gen-v3"}},
            {"kind": "lesson.close", "scope_type": "lesson", "scope_id": "019230a3-0000-7c0a-9a1f-000000000001", "payload": {"lesson_id": "019230a3-0000-7c0a-9a1f-000000000001", "epoch": 1}},
            {"kind": "backup.run", "scope_type": "system", "scope_id": None, "payload": {}},
        ]
        if tasks:
            v = V(tasks)
            bad = [ex["kind"] for ex in task_examples if not v.is_valid(ex)]
            if bad:
                fail(f"task examples invalid: {bad}")
            else:
                ok(f"{len(task_examples)} task examples validate")
        print("Accepted review invariants (ADR-014/016)")

        def rejects(label, schema, value):
            if V(schema, format_checker=V.FORMAT_CHECKER).is_valid(value):
                fail(f"negative contract accepted: {label}")
            else:
                ok(f"rejects {label}")

        invalid_rubric = copy.deepcopy(examples["rubric.default.json"])
        invalid_rubric["criteria"][0]["kind"] = "llm_rule"
        rejects("mixed evaluator kind", schemas["rubric.schema.json"], invalid_rubric)
        invalid_scenario = copy.deepcopy(examples["scenario.example.json"])
        invalid_scenario["reference"]["critical_errors"] = []
        rejects("obsolete critical_errors", schemas["scenario.schema.json"], invalid_scenario)
        known = {c["id"] for c in examples["rubric.default.json"]["criteria"]}
        overrides = examples["scenario.example.json"]["reference"].get("scoring", {})
        used = set(overrides.get("weights", {})) | set(overrides.get("critical", [])) | set(overrides.get("disabled", []))
        if not used <= known:
            fail(f"unknown scenario criteria: {used - known}")
        else:
            ok("scenario scoring references known rubric criteria")
        bad_evidence = copy.deepcopy(examples["evidence.example.json"])
        bad_evidence["events"][0]["delivered_at"] = None
        rejects("delivered event without delivered_at", schemas["evidence.schema.json"], bad_evidence)
        bad_evidence = copy.deepcopy(examples["evidence.example.json"])
        del bad_evidence["actions"][0]["action_id"]
        rejects("evidence action without stable id", schemas["evidence.schema.json"], bad_evidence)
        bad_scenario = copy.deepcopy(examples["scenario.example.json"])
        bad_scenario["reference"]["timing"] = {"complete_s": 999}
        rejects("case overriding lesson timing", schemas["scenario.schema.json"], bad_scenario)
        # Срез 3 / ADR-017: pilot_completed close from accepted, without a full evidence example.
        pilot_evidence = copy.deepcopy(examples["evidence.example.json"])
        pilot_evidence["close_reason"] = "pilot_completed"
        pilot_evidence["final_reaction"] = "accepted"
        if V(schemas["evidence.schema.json"], format_checker=V.FORMAT_CHECKER).is_valid(pilot_evidence):
            ok("evidence accepts pilot_completed close_reason")
        else:
            fail("evidence must accept pilot_completed close_reason (ADR-017)")

        # Срез 6 / ADR-019: rubric.default.json must carry D_FIELD_CORRECTIONS
        # (ADR-017's set_card_field is otherwise never scored).
        if "D_FIELD_CORRECTIONS" in known:
            ok("rubric.default.json scores ADR-017 field corrections")
        else:
            fail("rubric.default.json is missing D_FIELD_CORRECTIONS (ADR-019)")
        bad_input = copy.deepcopy(examples["assessment-inputs.example.json"])
        bad_input["transcripts"] = [{"call_id": "019230a4-6b1e-7c0a-9a1f-3f2a1b2c3d4e", "recording_sha256": "a" * 64, "state": "ready"}]
        rejects("ready transcript without text/model/parameters", schemas["assessment-inputs.schema.json"], bad_input)

        # Resolve only the local refs used by these schemas; no remote network calls.
        def resolve(node):
            if isinstance(node, list):
                return [resolve(x) for x in node]
            if not isinstance(node, dict):
                return node
            if "$ref" in node:
                target = spec
                for part in node["$ref"][2:].split("/"):
                    target = target[part]
                return {**resolve(target), **resolve({k: v for k, v in node.items() if k != "$ref"})}
            return {k: resolve(v) for k, v in node.items()}

        if "spec" in locals():
            command_schema = resolve(spec["components"]["schemas"]["Command"])
            good_command = {"command_id": "019230a4-0000-7000-8000-000000000001", "expected_seq": 0,
                            "type": "call_end", "payload": {"call_id": "019230a5-0000-7000-8000-000000000001", "accepted_by": "Сидоров", "summary": "Бригада направлена", "recording": None}}
            if not V(command_schema, format_checker=V.FORMAT_CHECKER).is_valid(good_command):
                fail("valid call_end rejected")
            for field in ("call_id", "accepted_by", "summary", "recording"):
                invalid = copy.deepcopy(good_command)
                del invalid["payload"][field]
                rejects(f"call_end without {field}", command_schema, invalid)
            # Срез 3 / ADR-017: set_card_field только для the one allowlisted path.
            good_field_correction = {"command_id": "019230a4-0000-7000-8000-000000000002", "expected_seq": 1,
                                      "type": "set_card_field", "payload": {"path": "/card/address/okrug", "value": "ЮАО"}}
            if not V(command_schema, format_checker=V.FORMAT_CHECKER).is_valid(good_field_correction):
                fail("valid set_card_field rejected")
            else:
                ok("accepts set_card_field for the allowlisted path")
            bad_path = copy.deepcopy(good_field_correction)
            bad_path["payload"]["path"] = "/card/incident/description"
            rejects("set_card_field with a non-allowlisted path", command_schema, bad_path)
            bad_value = copy.deepcopy(good_field_correction)
            bad_value["payload"]["value"] = ""
            rejects("set_card_field with an empty value", command_schema, bad_value)
            no_value = copy.deepcopy(good_field_correction)
            del no_value["payload"]["value"]
            rejects("set_card_field without a value", command_schema, no_value)
            rejects("expert revision without base_revision", resolve(spec["components"]["schemas"]["AssessmentRevision"]), {"reason": "Исправление", "criteria": []})
            first_expert = {"reason": "Ручная оценка", "criteria": [], "base_revision": 0}
            if not V(resolve(spec["components"]["schemas"]["AssessmentRevision"])).is_valid(first_expert):
                fail("first expert must allow base_revision=0")
            else:
                ok("manual assessment request without auto")
            grade = {"id":"00000000-0000-4000-8000-000000000001", "item_id":"00000000-0000-4000-8000-000000000002", "revision":1, "input_id":"00000000-0000-4000-8000-000000000003", "kind":"auto", "status":"needs_review", "rubric_version":"dds/rubric-v1", "criteria":[], "critical_errors":[], "created_at":"2026-09-18T10:00:00Z", "score":None, "passed":None}
            grade_schema = resolve(spec["components"]["schemas"]["Assessment"])
            if not V(grade_schema).is_valid(grade):
                fail("needs_review must accept null score/passed")
            grade["score"] = 100
            rejects("needs_review with numeric final score", grade_schema, grade)
            if any("recompute" in path for path in spec["paths"]):
                fail("recompute API must be absent in MVP")
            else:
                ok("no recompute API in MVP")
            receipt = spec["components"]["schemas"]["Receipt"]
            if receipt["properties"]["outcome"]["enum"] != ["applied", "rejected"] or "replayed" not in receipt["required"]:
                fail("receipt must preserve outcome and carry separate replayed flag")
            else:
                ok("replay preserves original outcome")

        ev = examples["evidence.example.json"]
        log_seq = [a["log_seq"] for a in ev["actions"]]
        if log_seq != list(range(1, ev["cutoff_log_seq"] + 1)) or len({a["action_id"] for a in ev["actions"]}) != len(log_seq):
            fail("evidence example has ambiguous action order")
        else:
            ok("evidence example includes accepted/rejected actions with unique ordered ids")
        if any("recompute" in job.get("properties", {}).get("payload", {}).get("properties", {}) for job in tasks["oneOf"]):
            fail("task payload must not offer recompute in MVP")
        rubric = load_json("rubric.default.json")
        complete = next(c for c in rubric["criteria"] if c["id"] == "T_COMPLETE")
        if complete["params"].get("anchor") != "primary_at" or complete["params"].get("limit_key") != "complete_s":
            fail("completion timing must use first primary_at and frozen lesson policy")
        else:
            ok("completion timing uses primary_at and effective policy")
    except ImportError:
        fail("jsonschema not installed; schemas and review invariants were not checked")

    print("DDL")
    url = os.environ.get("DATABASE_URL")
    if url:
        try:
            subprocess.run(["psql", url, "-X", "-v", "ON_ERROR_STOP=1", "-q", "-f", str(HERE / "schema.sql")], check=True, capture_output=True, text=True)
            ok("schema.sql applied")
            subprocess.run(["psql", url, "-X", "-v", "ON_ERROR_STOP=1", "-q", "-f", str(HERE / "check-invariants.sql")], check=True, capture_output=True, text=True)
            ok("SQL invariants: one auto, expert priority/manual without auto, nullable needs_review, immutable inputs, historical report, event shape, waiting/cancelled")
        except subprocess.CalledProcessError as e:
            fail(f"schema.sql: {e.stderr.strip()[:500]}")
        except FileNotFoundError:
            fail("psql not found; requested DDL check was not executed")
    else:
        print("  skip DATABASE_URL not set")

    print()
    if failures:
        print(f"{len(failures)} failure(s)")
        return 1
    print("all checks passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
