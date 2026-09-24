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
- Operator 112 slices 112-1 (first text call), 112-2 (prepared caller questioning), 112-3 (incident types, profile cards 104/101, service suggestions, `card_only` cases), and 112-4 (combined call+cards `full_case`, and the single "notify and save" action replacing per-service dispatch for both `card_only` and `full_case`) are implemented. 112-3/112-4 await expert acceptance of cards and service rules. Items already in progress when 112-4 shipped keep their pre-ADR-023 route (`review_service_selection`/`complete_profile_case` for `card_only`, `dispatch_intake` for `incoming_call`) unchanged.
- The remaining 112 order (revised 2026-09-24, open decisions listed in the plan §3.1): 112-5 AI caller and local inference → 112-6 112 assessment (deterministic + post-hoc LLM) and reports → 112-7 scenario editor → 112-8 voice → 112-9 acceptance → 112-10 interaction with DDS (waits for DDS customer input). Repeat calls, callbacks, intro mode for 112, medical/police cards, and 112 recommendations are tech debt.
- DDS slices 8–11 (intro hints, STT/LLM assessment, recommendations, scenario authoring) are not started. Until DDS slice 11, DDS instructors use prepared, immutable scenarios.
- The same scenario version may be assigned to multiple participants. Each participant receives an independent run and item state.
- When a slice needs UI, stabilize its model, rules, and API first, then implement the UI before declaring the slice complete.

## Code map

- `cmd/emsim/` composes the application; product modules are `internal/auth`, `content`, `training`, `assessment`, `reporting`, `media`; `internal/platform/` holds PostgreSQL, the task queue, audit, HTTP, observability, and realtime.
- `internal/training/` owns lessons, commands, transactions, and evidence; exercise rules live in `internal/training/dds/` and `internal/training/operator112/`, selected by `exercise_type`.
- `internal/assessment/` owns rubrics, the evaluation pipeline, and expert revisions; DDS criteria are in `internal/assessment/dds/`; operator 112 currently uses the manual rubric `design-docs/contracts/rubric.operator112.json`.
- Operator 112 UI: `web/src/routes/trainee/Operator112Workplace.tsx` (call-based cases) and `Operator112ProfileCase.tsx` (`card_only` cases).

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
- ADR-003 (no model calls on the interactive path) still applies. An interactive AI caller for 112 requires a new ADR before implementation (slice 112-5); DDS keeps the ADR-003 restriction.
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
