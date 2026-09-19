# EmSim Coordination Log

This append-only journal lets Claude, Codex, and other coding models resume work without reconstructing recent decisions from chat history. It records durable handoff information, not every shell command or thought process.

## Rules for writers

- Read the latest relevant entries before changing code.
- Append one entry after a meaningful completed task, architectural decision, investigation, or blocker.
- Use an ISO 8601 timestamp with timezone and identify the model/tool when known.
- State facts: what changed, why, what was validated, and what remains.
- List only files materially changed by the entry.
- Record test commands and their actual result. Do not say “tests pass” if they were not run.
- Keep an entry concise; target 10–25 lines.
- Never include secrets, credentials, session cookies, personal data, chain-of-thought, or large raw command output.
- Do not edit or delete previous entries. If an entry is wrong, append a correction that references its timestamp.
- Git history remains the source for exact diffs. Architecture documents and accepted ADRs remain the source for product decisions.

## Entry template

```markdown
## YYYY-MM-DDTHH:MM:SS±HH:MM — actor/model — short task name

Status: completed | partial | blocked | decision
Slice: N or n/a

Changed:
- `path`: concise description of the material change.

Decisions:
- Decision and brief reason. Use “none” when no durable decision was made.

Validation:
- `command` — passed/failed/not run, with a short relevant result.

Open issues / risks:
- Concrete unresolved item, or “none”.

Next:
- The next concrete action another model can take.
```

## Entries

## 2026-09-19T13:23:00+03:00 — Codex — establish cross-model coordination

Status: completed
Slice: n/a

Changed:
- `CLAUDE.md`: added persistent project, architecture, workflow, validation, and logging instructions for Claude Code.
- `LOG.MD`: created this append-only cross-model handoff journal and entry format.

Decisions:
- Project instructions are written in English for compact technical phrasing; product documentation and log entries may remain in Russian.
- `CLAUDE.md` is the exact filename because Claude Code documents that case-sensitive name.
- The delivery plan is defined in `slice-planning.md`; slices 1–7 form the DDS MVP.
- UI is implemented at the end of every slice that has a user-facing workflow, before that slice is considered complete.
- Prepared immutable scenarios are used until the final scenario-authoring slice. One scenario version may be assigned independently to many participants.

Validation:
- Documentation-only change; code tests were not run.

Open issues / risks:
- Product implementation of slice 1 has not started.

Next:
- Begin slice 1 by reconciling the auth/workstation migration and API contract with the accepted ADRs, then implement backend behavior before the slice UI.

## 2026-09-19T14:10:00+03:00 — Claude Opus 5 — review of slice plan against RFC/ADR/contracts

Status: decision
Slice: n/a

Changed:
- `slice-planning.md`: added cross-cutting rules (exercise_type and `training/dds` from first appearance, intro accepted by backend from slice 3, minimal functional UI per slice with ARM-112 visual pass deferred); slice 2 now seeds services/classifier and defines `emsim import scenarios`; assignment requires user_id; slice 4 now includes `lesson.close` worker task, API restart recovery, control_report; slice 6 bumps `trainee_assessment_state.version`; W0 measurement is a precondition of slice 9; new slice 12 (visual pass, load, failure checks, offline package).

Decisions:
- `assignments.user_id` is mandatory in MVP; "whoever logs in at the workstation" is not supported. Contracts (schema/openapi) to be tightened in slice 3.
- Prepared scenarios are loaded as `approved` via CLI; no editor until slice 11.
- Voice assets for the phone panel (slice 5) and instructor recording download endpoint: not decided yet; revisit before slice 5.

Validation:
- Documentation-only change; no tests run.

Open issues / risks:
- Slice 6 vs 9 boundary unresolved: per RFC §7.4 the deterministic auto assessment (rev=1) is produced by worker task `assessment.evaluate` over sealed `assessment_inputs`, which the plan currently places in slice 9. Awaiting user decision.

Next:
- Resolve the slice 6/9 boundary, then start slice 1 backend (auth, users, workstations, sessions) against the accepted contracts.

## 2026-09-19T14:25:00+03:00 — Claude Opus 5 — slice 6/9 boundary decided

Status: decision
Slice: 6, 9

Changed:
- `slice-planning.md`: slice 6 now includes the full auto-assessment pipeline without STT/LLM (evaluate task in waiting at close, coordinator sealing `assessment_inputs`, single `auto rev=1` as `needs_review` with NULL score, shared finalizer, expert without auto, cancellation of pending evaluate); slice 9 only adds `stt.transcribe`, dependencies, and the LLM judge over the existing input.

Decisions:
- Deterministic rules are never computed synchronously in the API; they live in the worker task per RFC §7.4 / ADR-016 B2.

Validation:
- Documentation-only change; no tests run.

Open issues / risks:
- none new; voice assets and instructor recording download remain open until slice 5.

Next:
- Start slice 1 backend (auth, users, workstations, sessions) against the accepted contracts.
