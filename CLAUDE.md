# EmSim Project Instructions

## Project

EmSim is an offline training system for emergency-service dispatchers with two separate exercises: DDS card processing (`dds_processing`) and operator 112 intake (`operator112_intake`). A DDS dispatcher receives a filled incoming card and processes it; an operator 112 answers an incoming call and builds the card from scratch. Keep their scenarios, commands, state, evidence, and rubrics separate; share only platform mechanisms (lessons, command protocol, journal, stop/recovery, assessment pipeline, reporting).

The application is a modular Go monolith delivered as one `emsim` binary with `migrate`, `api`, `worker`, `bootstrap-admin`, and `import` commands. PostgreSQL 16 stores product data and the durable background-task queue. The TypeScript/React SPA in `web/` is built and served by the API process. `seed/` holds prepared scenarios and media.

## Sources of truth

Read only the documents relevant to the current task. Use this precedence when they disagree:

1. The user's current instruction.
2. Accepted ADRs in @design-docs/adr/, especially newer ADRs that amend older ones (operator 112: ADR-021, ADR-022).
3. @design-docs/rfc-001-emsim.md and executable contracts in @design-docs/contracts/.
4. Delivery order and slice-level Definition of Done: @slice-planning-112.md and its detailed `slice-112-N-plan.md` files for operator 112; @slice-planning.md for DDS. The 112 plan supersedes the "DDS first, 112 later" ordering in `slice-planning.md` and ADR-015; the exercise separation from ADR-015 still applies.
5. `README.md`, `web/README.md`, `seed/README.md` for how to run and current user-visible behavior.
6. @docs/ for discovery and historical background. @docs/architecture/ is not the current implementation specification.

Do not silently resolve a material contradiction. Record the chosen interpretation in `LOG.MD` and update the relevant source-of-truth document when the task authorizes it. Plans describe future work too: never present a planned capability as implemented.

## Current delivery state

- DDS slices 1–7 (the DDS MVP) and the ARM-112 visual pass for DDS screens are implemented. By the user's decision, operator 112 work started before the remaining DDS slices; DDS resumes after the agreed 112 scope, using customer clarifications that are still pending.
- Operator 112 slices 112-1 (first text call), 112-2 (prepared caller questioning), 112-3 (incident types, profile cards 104/101, service suggestions, `card_only` cases), 112-4 (combined call+cards `full_case`, and the single "notify and save" action replacing per-service dispatch for both `card_only` and `full_case`), 112-5a (free-text caller chat window for `full_case` items with `caller_mode: "free_text"`, backed by a deterministic six-phrase stub adapter and the async `caller.reply` worker protocol from ADR-024), 112-5b stage 1 (an AI `CallerReplier` — `internal/training/operator112/aicaller` over an OpenAI-compatible `internal/platform/llm` client, selected by `CALLER_REPLIER=llm`; ADR-025 narrowly permits this one model call on the interactive path, ADR-003 unchanged for everything else; ADR-024's protocol is unchanged either way), and 112-6's deterministic stage (`operator112/rubric-v2`, ADR-026 — see below) are implemented. 112-3/112-4 await expert acceptance of cards and service rules. 112-5b stage 2 (ADR-029, 2026-09-27): its development part is implemented — `compose.yaml`'s `llm` service (`llama-server`, image pinned by digest, GGUF read-only from `./models` fetched by `make model`/`scripts/fetch-model.sh`, internal network only), `CALLER_REPLIER`/`ASSESSMENT_JUDGE` defaulting to `llm` in compose and in `internal/platform/config` (a missing endpoint/model is a startup error; `stub`/`off` only when set explicitly — `compose.no-llm.yaml`, which e2e/CI use), the `emsim bench-llm` W0 tool (`cmd/emsim/bench_llm.go`), and a best-effort prompt-cache warm-up (`caller.warmup`, enqueued by the api at `answer_incoming` — system prompt — and at the first operator message — that line plus the scenario's opening; `CALLER_WARMUP`, default on; it never touches the item, evidence or journal) with an optional `CALLER_OPENING_DELAY` holding the no-model opening back (default 0). The W0 measurement itself on the target server and the class-capacity decision are still open; the slice is not considered closed until they land ([`slice-112-5b-plan.md`](slice-112-5b-plan.md)). Items already in progress when 112-4 shipped keep their pre-ADR-023 route (`review_service_selection`/`complete_profile_case` for `card_only`, `dispatch_intake` for `incoming_call`) unchanged.
- 112-6 (deterministic stage, 2026-09-26): every new operator112 lesson freezes `operator112/rubric-v2` ([ADR-026](design-docs/adr/026-operator112-assessment-rubric-v2.md)) — 6 scored blocks (address, profile cards, caller topics, two timing blocks, description) plus 4 penalty criteria, scored against the *notified* (or, absent a notification, final) card snapshot compared to the scenario's own `intake112.reference`, never the raw transcript. No seed currently fills that reference; by ADR-026's own "reference absent" rule, a block or penalty that depends on it scores a plain 0 through the same business logic, not `not_applicable`/`needs_review` — so `auto rev=1 ready` is produced even before any seed gets a real answer key. `internal/assessment/operator112` is the evaluator; `internal/reporting` surfaces the same block/penalty breakdown in the instructor review, the lesson report, CSV, and PDF.
- 112-6, LLM stage (2026-09-26, ADR-028, ahead of 112-5b stage 2 by the user's own decision — see `slice-112-6-llm-plan.md`): `operator112/rubric-v3` adds one `kind: llm` criterion, `DESCRIPTION_CONTENT`, replacing `DESCRIPTION_PRESENT`'s "non-empty = 10" stub with a real judge of the field's own content against `intake112.reference.description_questions` — the model gets only the description text and the closed questions (never the transcript or the answer key), answers each `yes`/`no`/`needs_review`, and the backend (not the model) splits the criterion's weight equally across questions. `content.Operator112RubricVersion(judgeEnabled)` — not `RubricVersionFor`, which stays the safe rubric-v2 default — is what `training.Service.CreateLesson`/`StartPreview` freeze into a new 112 lesson, driven by each process's own `ASSESSMENT_JUDGE` (`llm` default since ADR-029, `off` explicit; `internal/platform/config`'s `API`/`Worker`, both needed since api decides the rubric version and only the worker ever wires a model). `internal/assessment`'s `RuleEvaluator.Evaluate` gained a `SemanticAnswers` parameter and two new optional interfaces, `SemanticPreparer`/`SemanticJudge` (plus `SemanticJudgeRegistry`/`JudgeConfig` on `Service`) — `internal/assessment/operator112` implements the first, `internal/assessment/operator112/descjudge` (an `internal/platform/llm.Client` adapter; `llm.Request` gained `ResponseFormat` for structured JSON output) the second, registered in `cmd/emsim/worker_composition.go`'s `judgeConfigFor` only under `ASSESSMENT_JUDGE=llm`. `Service.Handle` now calls the judge *outside* any transaction (`answerSemantic`, ADR-003/025's own convention — a failure becomes a retryable `judge_unavailable` `tasks.HandlerFailure`) before `recordAutoTx` folds the answer into the same attempt's `Evaluate` call; a `needs_review` answer (or exhausted retries) makes the whole criterion — and so the whole assessment — `needs_review` with no score, never a zero. Prompt/scoring rules are carried over verbatim from the user's own archived prototype (`handoff/claude_evaluator_112_20260926.zip`, 94.8% agreement on 132 descriptions); questions were added, as a new version 2, only to the three scenarios that already matched that prototype's own test cases (`seed/scenarios/pilot-112-ai-{car-in-water,mobile-shop,toyota-fire}-01-v2.json`) — the archive's other 19 scenarios are out of scope. `rubric.operator112.json` (v2) is unchanged; v3 lives in its own `rubric.operator112.v3.json`.
- 112-7 (scenario editor, 2026-09-26): an instructor creates, copies, validates, previews, and approves an `operator112_intake` scenario entirely through the web UI, without editing JSON — see [ADR-027](design-docs/adr/027-operator112-scenario-editor.md) and [`slice-112-7-plan.md`](slice-112-7-plan.md). Scoped to `mode="full_case"` + `caller_mode="free_text"` (AI caller) only; prepared-dialogue and `card_only` scenarios remain file-import-only. Every `PUT /scenarios/{id}` always creates a new draft version (never edits in place) with optimistic-concurrency `base_digest`/`409 stale_draft`; a draft is visible only to its own author (`GET /scenarios/{id}` falls back to an owner-only read on `ErrNotFound`), an approved version is visible to every instructor, and anyone may copy an approved scenario into their own new draft. "Пройти самому" starts a one-off `lessons.mode="preview"` lesson (migration `00017`, nullable `workstation_id`) with the author as its sole participant and no workstation — driven through the exact same trainee-facing item endpoints and UI (`Operator112ProfileCase`/`CallerChat`, reused unmodified) an ordinary trainee uses. Closing a preview item still enqueues a real `operator112/rubric-v2` auto-assessment (`enqueueEvaluateWaiting` fires for `ModePreview` too), but preview is invisible to `ListLessonsByInstructor`, `lesson_report_rows` (and everything built on it — reports, CSV/PDF, `/my/results`, `/my/progress`), and `trainee_assessment_state`'s version (the recommendation basis). `internal/content/editor.go` is the service layer; `internal/content/validate_detailed.go`'s `ValidateDetailed` is the same first-error `Validate` plus a multi-issue collector for editor-specific authoring checks (unreachable expected field, profile outside expected types, services diverging from `service_rules`, ambiguous ask pattern) — `error`-severity issues block preview/approve, `warning` does not.
- The remaining 112 order (revised 2026-09-26 — 112-6's LLM stage landed ahead of 112-5b stage 2 by explicit user decision, superseding the 2026-09-24 ordering below for that one item; open decisions otherwise still listed in the plan §3.1): 112-5b stage 2's remaining on-site part (W0 measurement with `bench-llm` on the target server and the class-capacity decision) → 112-8 voice → 112-9 acceptance → 112-10 interaction with DDS (waits for DDS customer input). Repeat calls, callbacks, intro mode for 112, medical/police cards, 112 recommendations, prepared-dialogue/`card_only` authoring through the 112-7 editor, and the archived prototype's remaining 19 description-judge scenarios are tech debt.
- DDS slices 8–11 (intro hints, STT/LLM assessment, recommendations, scenario authoring) are not started. Until DDS slice 11, DDS instructors use prepared, immutable scenarios.
- The same scenario version may be assigned to multiple participants. Each participant receives an independent run and item state.
- When a slice needs UI, stabilize its model, rules, and API first, then implement the UI before declaring the slice complete.

## Code map

- `cmd/emsim/` composes the application; product modules are `internal/auth`, `content`, `training`, `assessment`, `reporting`, `media`; `internal/platform/` holds PostgreSQL, the task queue, audit, HTTP, observability, and realtime.
- `internal/training/` owns lessons, commands, transactions, and evidence; exercise rules live in `internal/training/dds/` and `internal/training/operator112/`, selected by `exercise_type`.
- `internal/assessment/` owns rubrics, the evaluation pipeline, and expert revisions; DDS criteria are in `internal/assessment/dds/`; operator 112 criteria are in `internal/assessment/operator112/` (rubric `design-docs/contracts/rubric.operator112.json`, currently `operator112/rubric-v2` per ADR-026 — the pre-112-6 manual-only rubric is kept as `rubric.operator112.v1.json` for lessons frozen on it; `rubric.operator112.v3.json`, ADR-028, adds the one `kind: llm` criterion, `DESCRIPTION_CONTENT`, only ever frozen into a lesson when `ASSESSMENT_JUDGE=llm`). `internal/assessment/operator112/descjudge` is that one criterion's model adapter (`assessment.SemanticJudge` over `internal/platform/llm`), registered into `Service.judge` (`assessment.JudgeConfig`) only in the worker process. `internal/content/normalize` holds the case/ё/punctuation/word-order-insensitive comparison shared between scenario import validation and 112-6's scoring rules.
- Operator 112 UI: `web/src/routes/trainee/Operator112Workplace.tsx` (pre-112-3 `incoming_call` cases) and `Operator112ProfileCase.tsx` (`card_only` and `full_case` cases, including `CallerChat.tsx`'s free-text caller chat window for 112-5a/112-5b). `ItemReview.tsx` shows each caller turn's source (opening/scripted/model/fallback/stub, 112-5b) and, for a model or fallback reply, the model and prompt version from its `generation`; for `operator112_intake` it also renders 112-6's own read-only blocks/penalties auto-assessment tables (`components/IntakeAutoAssessment.tsx`, shared with the 112-7 preview screen — expandable per-field/per-card details, including DESCRIPTION_CONTENT's own per-question rows under ADR-028) instead of DDS's status table, a numeric-points expert-revision form instead of DDS's met/partial/not_met selects, and (when set) the judge's own model name from `assessment.model`. 112-7's editor: `routes/instructor/ScenarioEditor.tsx` (`/instructor/scenarios/new` and `/edit`, a five-tab form — the "Эталон" tab includes ADR-028's own description-question list) and `ScenarioPreview.tsx` (`/instructor/preview/:itemId`, reuses `Operator112ProfileCase` as-is for the author's own preview run); `ScenarioDetail.tsx`'s operator112 branch reads straight from `GET /scenarios/{id}`'s own body rather than the separate `/preview` endpoint, since that endpoint 404s for an unapproved draft.

## Architecture boundaries

- Keep the modular monolith. Do not introduce network services between product modules.
- Product modules are `auth`, `content`, `training`, `assessment`, `reporting`, `media`, and `platform`.
- A module writes only its own tables. Cross-module access goes through explicit consumer-owned ports; avoid import cycles.
- Use a pragmatic hexagonal structure: domain rules are independent of HTTP, SQL, files, STT, and LLM; application services coordinate use cases and transactions; infrastructure implements narrow ports; composition lives in `cmd/emsim`.
- Preserve one database transaction where a domain change, audit record, notification, and related background-task enqueue must be atomic.
- Interactive trainee commands are short API transactions. Do not route every card action through the worker queue.
- `platform/tasks` is the technical background queue. Lesson assignments and card order are domain data in `assignments`, `runs`, and `items`.

## orchestration-core reuse

- Reuse only the adapted code already copied into @internal/platform: task queue, worker runner, recovery, PostgreSQL bootstrap, observability, and admin endpoints.
- Treat @orchestration-core-main/ and its zip as read-only provenance/reference material. Do not import from it or modify it unless the user explicitly requests provenance work.
- Do not reuse the old product model (`runs`, `dialogue`, `judge`) as EmSim's training model.
- Preserve the queue guarantees: PostgreSQL clock for leases, fencing, at-least-once delivery, idempotent terminal outcomes, bounded retries, cancellation, and atomic domain effect plus task completion.

## Domain invariants

- Approved scenario versions and sealed evidence are immutable.
- Never expose scenario answer keys, scoring references, or instructor-only fields through trainee API responses.
- Server/PostgreSQL time is authoritative for training deadlines and ordering.
- Commands use stable `command_id`, request digest, and optimistic `expected_seq`. Replays must return the original outcome without a second effect.
- A lesson stop is a barrier: an effect is either committed before the barrier and included in evidence, or rejected/cancelled after it.
- Manual assessment must remain possible without STT or LLM.
- Expert assessment revisions take precedence over late automatic results and retain immutable history.
- Sent 112 card snapshots are immutable. History, progress, and reports never mix DDS and 112 results.
- ADR-003 (no model calls on the interactive path) still applies to DDS and to every other 112 model call (`scenario.generate`, `voice.render`, `stt.transcribe`). ADR-025 and ADR-028 carve out two narrow, non-interactive exceptions: 112's `caller.reply` task may call a model (112-5b stage 1, `internal/training/operator112/aicaller`), since it already runs outside the command transaction per ADR-024; and, for `assessment.evaluate`, `Service.Handle`'s own `answerSemantic` step may call a judge for `operator112_intake`'s `DESCRIPTION_CONTENT` criterion (112-6's LLM stage, `internal/assessment/operator112/descjudge`), always after the item closed and always outside the transaction that records the auto assessment. Both default to on since ADR-029 (the model ships as compose's `llm` service); `CALLER_REPLIER=stub`/`ASSESSMENT_JUDGE=off`, set explicitly, keep 112-5a's/112-6's own no-model behavior.
- Never expose to trainees the answer key, hidden caller facts, future caller lines, or the full dialogue tree.

## Implementation workflow

1. Read the latest relevant entries in `LOG.MD`, then inspect the affected code and current git status.
2. Preserve unrelated user changes in the dirty worktree. Never reset, discard, or rewrite them.
3. Implement the smallest complete vertical behavior for the active slice, including migrations, contracts, backend, tests, and UI when applicable.
4. Update OpenAPI and schema contracts in the same change as externally visible behavior.
5. Run focused tests while iterating. Before declaring implementation complete, run `make verify` (and `make verify-web` for client changes); run `make test-integration` when PostgreSQL task/recovery or cross-process behavior changes.
6. Append one concise handoff entry to `LOG.MD` after meaningful work or before handing the repository to another model.
7. Do not loosen tests

## Go and database conventions

- Format Go with `gofmt`; use standard-library patterns unless a dependency provides clear value.
- Keep interfaces narrow and declare them near the consuming application service.
- Return safe public errors; keep operational detail in structured logs.
- Use forward-only migrations and PostgreSQL 16 semantics.
- Use `pgx.Tx` for atomic domain and queue operations. The caller owns commit and rollback.
- Prefer meaningful domain and integration tests. Do not add tests that merely duplicate implementation details.

## Commands

```bash
make verify
make verify-web
make test-integration
make compose-config
python3 design-docs/contracts/check.py
cd web && npm run lint
cd web && npm run test:e2e
docker compose up --build
```

`make test-integration` requires Docker or a separate empty PostgreSQL database supplied through `TEST_DATABASE_URL`. Run `make verify-web` and lint for client changes, the contracts check for schema/OpenAPI changes, and browser e2e when a user workflow changes. Report which checks were actually run.

## Coordination log

`LOG.MD` is the cross-model handoff journal, not a transcript. Before work, read its newest relevant entries. Append an entry only for a meaningful implementation, decision, investigation, or blocker. Include changed files, validation actually run, unresolved risks, and the next concrete step. Never write secrets, credentials, cookies, personal data, large command output, or speculative claims into the log. Do not rewrite or delete older entries; add a correction entry when necessary.


## Commits
You may commit changes when user asks for it. But you MUST never commit changes as author, so if commit is pushed to github you should not be listed as co-author or contributor
