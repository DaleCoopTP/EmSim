# EmSim Project Instructions

## Project

EmSim is an offline training system for emergency-service dispatchers. The first product stage implements the DDS card-processing exercise (`dds_processing`). Operator 112 intake is a later stage and must not be mixed into the DDS implementation.

The application is a modular Go monolith delivered as one `emsim` binary with `migrate`, `api`, and `worker` commands. PostgreSQL 16 stores product data and the durable background-task queue. The web client will be a TypeScript/React SPA served by the API process.

## Sources of truth

Read only the documents relevant to the current task. Use this precedence when they disagree:

1. The user's current instruction.
2. Accepted ADRs in @design-docs/adr/, especially newer ADRs that amend older ones.
3. @design-docs/rfc-001-emsim.md  and executable contracts in @design-docs/contracts/.
4. @slice-planning.md for delivery order and slice-level Definition of Done.
5. @docs/ for discovery and historical background. @docs/architecture/ is not the current implementation specification.

Do not silently resolve a material contradiction. Record the chosen interpretation in `LOG.MD` and update the relevant source-of-truth document when the task authorizes it.

## Current delivery plan

- Implement vertical slices in the order defined by @slice-planning.md.
- Slices 1–7 form the DDS MVP.
- Until slice 11, instructors use prepared, immutable scenarios and cannot create or edit scenario content.
- The same scenario version may be assigned to multiple participants. Each participant receives an independent run and item state.
- STT and LLM assessment belong to slice 9. Recommendations belong to slice 10. Scenario authoring and generation belong to slice 11.
- When a slice needs UI, stabilize its model, rules, and API first, then implement the UI before declaring the slice complete.

## Architecture boundaries

- Keep the modular monolith. Do not introduce network services between product modules.
- Product modules are `auth`, `content`, `training`, `assessment`, `reporting`, and `platform`.
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

## Implementation workflow

1. Read the latest relevant entries in `LOG.MD`, then inspect the affected code and current git status.
2. Preserve unrelated user changes in the dirty worktree. Never reset, discard, or rewrite them.
3. Implement the smallest complete vertical behavior for the active slice, including migrations, contracts, backend, tests, and UI when applicable.
4. Update OpenAPI and schema contracts in the same change as externally visible behavior.
5. Run focused tests while iterating. Before declaring implementation complete, run `make verify`; run `make test-integration` when PostgreSQL task/recovery or cross-process behavior changes.
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
make test-integration
make compose-config
docker compose up --build
```

`make test-integration` requires Docker or a separate empty PostgreSQL database supplied through `TEST_DATABASE_URL`.

## Coordination log

`LOG.MD` is the cross-model handoff journal, not a transcript. Before work, read its newest relevant entries. Append an entry only for a meaningful implementation, decision, investigation, or blocker. Include changed files, validation actually run, unresolved risks, and the next concrete step. Never write secrets, credentials, cookies, personal data, large command output, or speculative claims into the log. Do not rewrite or delete older entries; add a correction entry when necessary.


## Commits
You may commit changes when user asks for it. But you MUST never commit changes as author, so if commit is pushed to github you should not be listed as co-author or contributor
