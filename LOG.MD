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

## 2026-09-22T00:33:59+03:00 — Codex — debt closure before ARM UI work

Status: completed
Slice: n/a

Changed:
- `Makefile`, `go.mod`, `cmd/emsim/api.go`: updated Staticcheck and isolated its cache; `make verify` once again checks real production packages under the pinned Go toolchain.
- `design-docs/contracts/{sse-events.schema.json,check.py}` and `test/integration/training_realtime_e2e_test.go`: contract now describes the actual `stream.ready`, `resync`, and `invalidate` wire frames including cursor payloads.
- `design-docs/contracts/scenario.schema.json`, ADR-018 and training/content validation: suspended unschedulable `spawn_card kind=duplicate`; legacy JSON stays decodable but new import and assignment reject it.
- `web/{playwright.config.ts,e2e/,src/routes/trainee/Workplace.tsx}` and CI: Chromium acceptance coverage now uses a fake WAV microphone, proves track-level Mute, waits for `MediaRecorder.onstop`, retries a failed upload without replaying `call_end`, and closes the card.
- `README.md`, `web/README.md`: documented the isolated browser e2e command and local Chromium installation.

Decisions:
- `duplicate` remains deferred until scenario authoring has a finite, explicit queue model; only stable prepared-scenario references are public today.

Validation:
- `GOCACHE=/private/tmp/emsim-gocache make verify` — passed.
- `python3 design-docs/contracts/check.py` — passed.
- `GOCACHE=/private/tmp/emsim-gocache go test -tags=integration -count=1 ./test/integration -run TestAPIProcessSSEStreamSnapshotReplayResyncAndFiltering` — passed.
- `cd web && npm run check && npm run lint && npm run build` — passed.
- `cd web && npm run test:e2e` — passed; full card → call → recording retry → close path in Chromium, temporary compose stack removed.

Open issues / risks:
- UI styling and real ARM/phone visual fidelity are the next product phase, not part of this debt pass.

Next:
- Start the ARM-aligned UI implementation after product references for the actual dispatcher workstation and phone are available.

## 2026-09-21T22:40:00+03:00 — Codex — slice 7 reporting acceptance

Status: completed
Slice: 7

Changed:
- `internal/reporting/pdf.go`: reserved body space below the PDF header, fixing title/summary overlap found by rendered-page QA.
- `test/integration/reporting_e2e_test.go`: added API → report worker → immutable PDF/blob and trainee-projection acceptance coverage.
- `test/integration/{training_e2e_test.go,worker_process_test.go}`: shared the API blob root with worker tests and configured the required report pool.
- `README.md`: documented instructor exports, immutable PDF snapshots, trainee history and the acceptance path.

Decisions:
- A PDF artifact is verified as a worker-produced blob, while JSON and CSV remain current projections.

Validation:
- `go test -tags=integration ./test/integration -run TestReportingThroughAPIAndWorker -count=1` — passed.
- Temporary rendered A4 PDF inspected with `pdftoppm` after the layout fix — header, summary, body and footer no longer overlap.

Open issues / risks:
- none.

Next:
- Begin the next planned slice.

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

## 2026-09-19T17:40:00+03:00 — Claude Sonnet 5 — slice 1 backend complete

Status: completed
Slice: 1

Changed:
- `internal/platform/httpapi`, `internal/platform/audit` (+`migrations/00002_platform_audit_log.sql`): shared request-id/no-store/Origin-check/error-envelope middleware; transactional audit writer.
- `internal/auth` (+`postgres`, `+http`, `+migrations/00003_auth_users_workstations_sessions.sql`): domain, argon2id passwords, authz table, login/logout/me/session middleware, admin users+workstations CRUD, `BootstrapAdmin`.
- `cmd/emsim/bootstrap.go`: `bootstrap-admin` subcommand, idempotent (no-op once an active admin exists); wired into `main.go` dispatch.
- `compose.yaml` (+`.env.example`): one-shot `bootstrap` service between `migrate` and `api`; `api` gets `COOKIE_SECURE=false` for the TLS-less demo profile.
- `test/integration/api_process_test.go`: builds the real binary and drives `migrate → bootstrap-admin (×2) → api` purely over HTTP — admin login, workstation/instructor/trainee creation, logout invalidation, trainee login with workstation, session survives a simulated reload, 403 for a non-admin on `/admin/*`, wrong-password 401.
- `README.md`: demo quick start with `bootstrap`, curl walkthrough, description of both process-level integration tests.

Decisions:
- `BootstrapAdmin` lives on `auth.Service` (audits `auth.bootstrap_admin` with a nil actor), not as ad hoc SQL in `cmd/emsim`, so it shares validation/hashing/audit with the rest of user creation.
- Backend-only slice 1 (no SPA yet, commits C0–C7 of the approved plan); UI (C8 web scaffold, C9 login/admin screens) is the remaining Definition-of-Done item before the slice counts as done end-to-end.

Validation:
- `make verify` — passed.
- `make test-integration` — passed (includes the new `TestAPIProcessAuthHappyPath`, run standalone in 3.7s).
- `python3 design-docs/contracts/check.py` — passed.

Open issues / risks:
- No web client yet: slice-planning.md's Definition of Done for slice 1 requires a working UI before the slice is considered complete (C8/C9 still pending).

Next:
- C8: SPA scaffold (Vite+React+TS, `openapi-typescript` types, `web/embed.go`, static+SPA-fallback serving from `api`), then C9: login and role-home/admin screens.

## 2026-09-19T17:57:00+03:00 — Claude Sonnet 5 — web SPA scaffold, embedded and served

Status: completed
Slice: 1

Changed:
- `web/`: Vite + React 18 + TypeScript (strict) + react-router-dom + @tanstack/react-query scaffold; `npm run generate:api` runs `openapi-typescript` against `design-docs/contracts/openapi.yaml` into a gitignored `src/api/schema.d.ts`; `src/api/client.ts` (thin fetch wrapper: `credentials: include`, closed `Error` envelope → `ApiError`), `src/api/useMe.ts`, route skeleton (`/login`, `/`) with placeholder screens — real login/admin/trainee screens are C9.
- `web/embed.go`: `//go:embed all:dist` behind a checked-in `web/dist/.gitkeep`, so `go build` never needs Node.
- `cmd/emsim/static.go` (+`api.go`): `newSPAHandler` mounted at `/` next to the API mux at `/api/` — serves a real file as itself (hashed `assets/*` get `Cache-Control: public, max-age=31536000, immutable`), falls back to `index.html` for any other path (client-side routing), and returns the same closed JSON 404 envelope as the rest of the API when `web/dist` has no build yet.
- `Dockerfile`: new `node:22-alpine` stage builds `web/dist` before the Go build stage copies it in; `.dockerignore` now allows `design-docs/contracts/openapi.yaml` through (needed by the web build) while still excluding the rest of `design-docs/`.
- `Makefile` (`web-install`/`web-build`/`web-check`/`verify-web`), `.github/workflows/verify.yml` (new `web` job, Node 22).

Decisions:
- `TestPublicHandlerServesJSONNotFoundWithNoStoreAndRequestID` (`cmd/emsim/api_test.go`) was moved from a bare `/anything` path to `/api/v1/anything`: a path outside `/api/` now goes through the SPA handler, whose response depends on whether `web/dist` has actually been built (it embeds whatever is on disk at compile time) — a test must not depend on that. `static_test.go` covers the SPA handler's own build/no-build behavior deterministically against a fake `fs.FS`.
- Verified against a real `docker compose up --build`: hashed asset caching, SPA fallback on `/login`, and the API's own 404 envelope on an unknown `/api/` path all behave as intended (manual check, not an automated test).

Validation:
- `make verify` — passed (Go side never touches Node).
- `make verify-web` — passed (npm ci, type generation, `tsc -b`, `vite build`).
- `make test-integration` — passed.
- `python3 design-docs/contracts/check.py` — passed.
- `docker compose up --build` — passed; manual curl/browser check of `/`, `/assets/*`, `/login`, `/api/v1/bogus`.

Open issues / risks:
- No actual login form or role screens yet (C9). Until then the SPA only proves the plumbing (routing, session round-trip via `useMe`, static serving).

Next:
- C9: login screen (`POST /auth/login`), role home screens (trainee: full name/service/workstation/"ожидайте назначения занятия"; admin: user + workstation management), client-side route protection via `useMe()`.

## 2026-09-19T18:25:00+03:00 — Claude Opus 5 — slice 1 complete: login, role homes, admin UI

Status: completed
Slice: 1

Changed:
- `web/src/routes/Login.tsx`: login form (login, password, optional workstation number — the server decides whether it is required); errors rendered from the closed `Error` envelope by code and `details.field/reason` (`web/src/api/errors.ts`).
- `web/src/routes/Home.tsx`: role home — admin → `/admin/users`; instructor → placeholder until slice 3; trainee → full name, service, workstation, «Ожидайте назначения занятия».
- `web/src/routes/admin/Users.tsx`, `Workstations.tsx`: paginated user table with create/edit forms (role, service, level, active, password change; PATCH sends only changed fields, `service_code:null` on role change away from trainee); workstation list editor saved with one `PUT /admin/workstations` (unticked rows are left out → deactivated by the server, never deleted).
- `web/src/components/RequireAuth.tsx`, `Layout.tsx`: client-side guard via `useMe()` (401 → `/login`, wrong role → `/`), header with identity and «Выйти»; `web/src/api/{auth,admin}.ts`: react-query mutations/queries.

Decisions:
- The client guard is a UX convenience only; every admin endpoint still enforces role on the server (checked: trainee session gets 403 from `GET /api/v1/admin/users` directly).
- Empty-patch edits (no field changed) are sent as `{}` and the server answers 200 unchanged; not worth a client-side short-circuit.

Validation:
- `make verify`, `make verify-web`, `oxlint` — passed.
- Manual happy path in the browser against `docker compose up --build` (slice-planning.md §2 DoD): wrong password → 401 text; admin login → users page; add РМ-05 via editor (PUT 200); create trainee with service (201); edit dialog: deactivating the only admin → 409 shown; logout → `/admin/users` bounces to login; trainee login without РМ → «Укажите номер рабочего места», unknown РМ → «не найдено», with РМ 5 → home with ФИО/служба/РМ/«Ожидайте назначения занятия»; F5 keeps the session; trainee on `/admin/users` → redirected, direct fetch → 403; logout → login page. Automated equivalent of the API side: `test/integration/api_process_test.go`.

Open issues / risks:
- Instructor home is a placeholder by design (no lessons before slice 3).
- Admin «не получает доступ к содержимому занятий и оценок» is enforced by the authz table (`internal/auth/authz.go` tests) but has no endpoints to exercise yet.

Next:
- Slice 2: prepared-scenario catalogue — `services`/`classifier_types` seed, `scenario_versions` (immutable, `exercise_type`), `emsim import scenarios <dir>`, instructor catalogue API + UI; answer key never exposed on trainee routes.

## 2026-09-19T18:50:00+03:00 — Claude Opus 5 — level is not an admin-editable account field

Status: decision
Slice: 1

Changed:
- `design-docs/contracts/openapi.yaml`: `level` removed from `UserCreate` and `UserPatch`; `User.level` documented as read-only training state.
- `internal/auth` (`domain.go`, `admin.go`, `http/admin.go` + tests): `NewUser`/`Patch` lost `Level`; every new user starts at `easy`. `UserUpdate.Level` (store port) stays for the slice-10 apply-level use case.
- `web/src/routes/admin/Users.tsx`: level dropped from create/edit forms, kept as a read-only column.

Decisions:
- `users.level` is the trainee's current difficulty — state produced by training (recommendation accepted by the instructor, RFC-001 §7.4, slice 10), not a provisioning attribute. The admin API therefore neither sets nor changes it; the only writer will be the apply-level application use case through the auth port.

Validation:
- `make verify`, `python3 design-docs/contracts/check.py`, `npm run check`, `npm run lint` — passed.

Open issues / risks:
- none.

Next:
- Slice 2 planning.

## 2026-09-19T19:16:00+03:00 — Codex — first-slice review fixes (selected findings)

Status: completed
Slice: 1

Changed:
- Auth logout now propagates transactional storage/audit failures; HTTP returns `500 internal_error` and keeps the cookie when revocation was not confirmed. Invalid and already-missing sessions remain idempotent `204`.
- Added the `auth.IdentityProvider` port with an injectable local Argon2id provider; production composition now selects it explicitly, preserving the LDAP/AD replacement seam from ADR-008.
- Protected routes refresh `emsim_session` from the authoritative server-side expiry, completing sliding-session renewal in the browser.
- Admin JSON parsing rejects top-level `null` and `null` for non-nullable patch fields; `PUT /admin/workstations` can no longer interpret `null` as an intentional empty replacement. OpenAPI reflects logout failure and cookie-renewal behavior.
- Admin user editing remounts the form by user id, preventing state/password values from leaking into the next selected user.
- HTTP metrics resolve registered inner-mux patterns before dispatch into bounded route slugs; successful health/readiness/metrics probes are no longer logged at INFO, while probe failures remain logged.
- `make staticcheck` now fails if Staticcheck exits successfully after matching no packages.

Decisions:
- Per the task scope, concurrent last-admin/profile updates, role/workstation session invalidation, and best-effort rejected-login audit were intentionally left unchanged.

Validation:
- `make verify` — passed with writable isolated Go/Staticcheck caches.
- `go test -race ./internal/auth/... ./internal/platform/observability ./cmd/emsim` — passed.
- `make test-integration` — passed.
- `python3 design-docs/contracts/check.py` — passed.
- `make verify-web`, `npm run lint` — passed.

Open issues / risks:
- The three intentionally deferred review findings above remain open.

Next:
- Decide whether to schedule the deferred slice-1 hardening before starting slice 2.

## 2026-09-19T20:58:49+03:00 — Codex — review and narrow slice-2 implementation plan

Status: completed
Slice: 2 (planning only)

Changed:
- `slice-2-plan.md`: reviewed replacement for the supplied C1–C7 plan; records the two pilot cases, structured correction reference, instructor preview, explicit version imports, migration/seed ordering and acceptance checks.
- `slice-planning.md`: linked the detailed plan and recorded the agreed pilot scope.

Decisions:
- Two cases suffice for the catalogue; full workflow, complete classifier and 8–12 cases are not prerequisites. Pilot correction targets existing `card.address.okrug`, not `district`.
- Plan requires explicit new versions, empty contacts for no-call cases, safe FK validation over existing users, preserved approval provenance after superseding, deletion protection and allowlisted public DTOs. These are planned implementation changes, not applied contracts.
- Acceptance-only completion remains a temporary pilot goal; existing ADR/RFC close rules remain in force until separately amended for slice 3. No trainee execution or evaluator is added in slice 2.

Validation:
- Read supplied plan and compared relevant local schema, OpenAPI, scenario format, auth role rules, Compose and slice DoD. No code, migration or runtime tests run for this documentation-only change; prior test/XLSX claims in the pasted plan were not revalidated.

Open issues / risks:
- Organizer confirmation of full workflow/edit permissions and deferred slice-1 findings remain outstanding; neither is silently marked resolved.

Next:
- Implement C1 from `slice-2-plan.md`, then proceed through C7; verify all contract and migration changes during implementation. No commits made.

## 2026-09-19T22:34:23+03:00 — Claude — slice 2 C1 (contracts/migration) and C2 (content domain)

Status: completed
Slice: 2 (`slice-2-plan.md`; execution detail in `/Users/lucifer/.claude/plans/melodic-toasting-pudding.md`)

Changed — C1 (contracts and migration):
- `design-docs/contracts/schema.sql`: `scenarios.source_key`; three-way `scenario_versions_approval_shape` CHECK preserving approval attribution across `approved→superseded`; partial unique index enforcing one `approved` version per scenario; `BEFORE DELETE` guard rejecting deletion of non-draft versions; `users.service_code_fkey → services(code)`.
- `design-docs/contracts/scenario.schema.json`: `contacts.minItems` 1→0; new `reference.field_corrections` and `reference.pilot_goal` (pilot extension, both optional).
- New `design-docs/contracts/scenario-file.schema.json` (file wrapper: key/version/title/origin/body) and `design-docs/contracts/embed.go` (embeds both schemas + `rubric.default.json` for the binary).
- `design-docs/contracts/openapi.yaml`: role notes, "not implemented until slice 11" markers on editor routes, `ScenarioSummary.source_key`, new `Address`/`CardPreview`/`ScenarioReference` schemas, typed `/scenarios/{id}/preview` response.
- `design-docs/contracts/check.py`: validates the new file schema too.
- New `migrations/00004_content_catalog.sql`: `services`, `classifier_types`, `tickets`, `scenarios`, `scenario_versions` + the two triggers above; FK added `NOT VALID` (validated explicitly once `services` is seeded, per the migration's header comment).
- `internal/platform/postgres/postgres.go`: `ExpectedSchemaVersion` 3→4, `applicationTables` extended.
- `test/integration/{harness,auth_store,api_process}_test.go`: updated table set; added `services` fixtures the new FK now requires.

Changed — C2 (`internal/content` domain + `internal/content/schema`):
- New package `internal/content`: typed `Body`/`Card`/`Address`/`Contact`/`Event`/`Reference`/`Workflow` etc. mirroring `scenario.schema.json` field-for-field; `DecodeFile` (duplicate-object-key rejection at any depth, `json.Number`-preserving raw form alongside the typed struct); `Canonical`/`Digest`/`BodyDigest` (deterministic serialization independent of key order/whitespace, numbers kept at their source literal); `Validate(body, catalog)` — semantic rules JSON Schema can't express (service/classifier existence and active state, exactly-one `mine`, contact/event key uniqueness, workflow reachability of `primary_decision`/`expected_chain`, `field_corrections.before_status` against the service's workflow, `scoring` references against `rubric.default.json`'s criterion ids); `ProjectCard`/`ProjectContacts` — the allowlist `CardPreview` projection (no `reference`/hints/generation/events-expects/phrases reach it).
- New package `internal/content/schema`: compiles `scenario-file.schema.json` + `scenario.schema.json` from the embedded contracts via `github.com/santhosh-tekuri/jsonschema/v6` (new dependency; `go.mod`/`go.sum` updated), offline only.
- `Validate` deliberately does not require `field_corrections.expected_value` to differ from the card's actual value — that mismatch is the pilot exercise itself, confirmed by a dedicated test.

Decisions:
- `Catalog` (the service/classifier lookup `Validate` needs) is a plain value-returning interface, not an `auth.Store`-style `ctx+pgx.Tx` port — `Validate` performs no I/O itself; C3's `Importer` will pre-fetch and adapt.
- `design-docs/contracts/embed.go` (called for in C1's plan but not written then) was added now, since C2 needed it; `contracts.Files` also carries `rubric.default.json` for `Validate`'s scoring check.

Validation:
- `go build ./...`, `go vet ./...`, `gofmt -l .` — clean.
- `go test ./...` — all packages pass (`internal/content`: 40/40 including duplicate-key/digest/leak/schema tests).
- `make verify` (format-check, build, test, vet, staticcheck) — passed.
- `python3 design-docs/contracts/check.py` — all checks passed.
- Manual SQL invariant checks against throwaway Postgres containers for every migration edge case (approval shape, one-approved index, content-immutability trigger, delete guard incl. cascade, `NOT VALID` FK over existing orphaned data, `VALIDATE CONSTRAINT` after seeding) — see prior session notes; not re-run this pass since the migration file did not change.
- `make test-integration` not re-run in this pass (no change to `test/integration` since the last green run recorded for C1).

Open issues / risks:
- C2 has no store/HTTP yet — `Catalog`, `Validate`, and the preview projection are exercised only by unit tests with an in-memory fake; real DB-backed behavior is unverified until C3.

Next:
- C3 — `internal/content/postgres` store, `Importer` application service, `emsim import services|classifier|scenarios|seed` CLI, and the actual seed files (two pilot scenarios). No commits made.

## 2026-09-19T22:54:37+03:00 — Claude — slice 2 C3 (storage, import CLI, pilot seed)

Status: completed
Slice: 2 (`slice-2-plan.md`, C3)

Changed:
- `internal/content/store.go`: `Store` port (ctx+pgx.Tx, internal/auth's convention), `ServiceRecord`/`ClassifierType`/`ScenarioRecord`/`ScenarioVersionRecord`/`ScenarioSummary`/`ScenarioDetail`/`VersionSummary`/`ScenarioFilter`, `ErrNotFound`/`ErrStorage`/`ErrConflict`.
- `internal/content/service.go`, `import.go`, `read.go`: `Service` (one struct for both use-case families, `internal/auth.Service`'s shape) — `ImportServices`/`ImportClassifierTypes` (insert-or-no-op-or-conflict), `ImportScenarios` (whole-batch transaction; per-file version rules: new key must start at v1, known key's title/target_service/origin are fixed, next version must be exactly max+1 and supersedes the previous approved version preserving its attribution, an existing version's content can't change, no version gaps); `ListServices`/`ListScenarios`/`ScenarioDetail`/`ScenarioVersions`/`ScenarioPreview`.
- `internal/content/postgres/store.go`: pgx-backed `Store`. Notable fix: `InsertScenarioVersion`'s `CASE WHEN status='approved' THEN $N` reusing the same placeholder for both a plain INSERT value and a CASE branch made PostgreSQL report "inconsistent types deduced" (and, once split, "column is uuid but expression is text") — fixed by giving `approved_by` its own parameter with an explicit `::uuid` cast.
- `internal/platform/postgres/constraints.go` (new): `OrphanServiceCodes`/`ValidateUsersServiceCodeFK` — cross-module (auth+content) helpers for the `users_service_code_fkey` NOT VALID → validated transition; `postgres.go` gained `UpTo` for tests that need to simulate "existing installation before migration 00004".
- `cmd/emsim/import.go` (new) + `main.go`: `emsim import services|classifier|scenarios|seed --actor <login> <path>`. `--actor` must resolve to an active admin. `import services` additionally checks for orphan `users.service_code` and only then runs `VALIDATE CONSTRAINT`, refusing with a listed set of codes otherwise.
- `seed/`: `services.json` (3 pilot services, identical minimal workflow), `classifier.json` (code `14080106`), `scenarios/pilot-tree-{01,02}.json` (the two pilot cases from slice-2-plan.md — 02 has the deliberate `okrug` error + `field_corrections`), `README.md`.
- `internal/content/seed_test.go`: the real seed files pass schema + semantic validation (no DB).

Validation:
- `go build ./...`, `go vet ./...`, `gofmt -l .` — clean.
- `go test ./...` — all green.
- `make verify` (staticcheck included) — passed.
- `go test -tags=integration -count=1 ./test/integration/...` — all green, including new `content_import_test.go` (seed import + idempotent replay + conflict rejection + version/supersede rules + batch rollback on one invalid file) and `content_fk_test.go` (orphan detection, `VALIDATE CONSTRAINT` fails while an orphan exists and succeeds once fixed, idempotent re-run).
- Manual end-to-end smoke test of the built `emsim` binary against a throwaway `postgres:16-alpine` container: `migrate up` → `bootstrap-admin` → `import seed` (services/classifier/scenarios all created, FK validated) → re-run `import seed` (all unchanged) → `import services --actor nobody` and bare `import` both fail cleanly with a safe exit code.

Open issues / risks:
- None new. C4 (instructor HTTP API) and C5 (auth linkage) are next.

Next:
- C4 — `internal/content/http` (GET /services, /scenarios, /scenarios/{id}, /scenarios/{id}/versions, /scenarios/{id}/preview), `authz.GroupServices`, composition in `cmd/emsim/api.go`.

## 2026-09-19T23:02:58+03:00 — Claude — slice 2 C4 (instructor HTTP API)

Status: completed
Slice: 2 (`slice-2-plan.md`, C4)

Changed:
- `internal/content/http/handlers.go` (new): `GET /services` (`authz.GroupServices`, admin+instructor), `GET /scenarios` (filters service/status/difficulty_min/difficulty_max, page/page_size, `difficulty_min>difficulty_max` → 422), `GET /scenarios/{id}`, `GET /scenarios/{id}/versions`, `GET /scenarios/{id}/preview` — all instructor-only (`authz.GroupContent`). Reuses `internal/auth/http`'s `SessionMiddleware`/`RequireRole` directly rather than reimplementing session handling. JSON DTOs are explicit (mirroring `internal/auth/http`'s `toUserJSON` convention) for every closed/allowlisted shape (`CardPreview` and friends); the full-body `Scenario`/`ScenarioBody` response reuses `content.Body` as-is since it is already the wire format (`internal/content`'s json tags) and this route is instructor-only.
- `internal/auth/authz.go`: new `GroupServices` (admin+instructor) distinct from `GroupContent` (instructor-only) — an admin needs the service list for `/admin/users` but not scenario content; covered in `authz_test.go`.
- `internal/content/store.go`, `internal/content/postgres/store.go`: `VersionSummary` gained `Difficulty` (`openapi.yaml`'s `ScenarioVersionSummary.difficulty` was missing from the domain type).
- `cmd/emsim/api.go`: composes `content.Service` + `contenthttp.Handlers` onto the same mux as auth, behind the shared public-API middleware; `mustSchemaValidator()` panics on a malformed embedded schema (build-time invariant, mirrors `auth/password.go`'s `mustHashDummy`).

Validation:
- `go build ./...`, `go vet ./...`, `gofmt -l .` — clean.
- `go test ./...` — all green, including new `internal/content/http` handler tests (auth/role boundaries for every route, filter application, 404 mapping, full-body detail includes the instructor-only reference, preview leak check, version list shape).
- `make verify` (staticcheck included) — passed.
- `go test -tags=integration -count=1 ./test/integration/...` — all green, including new `TestAPIProcessContentCatalogAccess`: real binary, `migrate up` → `bootstrap-admin` → `import seed` → running `api` process, driven over HTTP — instructor sees the catalogue and a leak-free preview (reference present, `card` has no `pilot_goal`), admin reaches `/services` but gets 403 on `/scenarios`, unauthenticated requests get 401.

Open issues / risks:
- None new.

Next:
- C5 — `ServiceCatalog` port in `internal/auth` (422 on an unknown `service_code`), adapter in `content`, web `<select>` for service in the admin Users form.

## 2026-09-19T23:11:26+03:00 — Claude — slice 2 C5 (auth-content linkage)

Status: completed
Slice: 2 (`slice-2-plan.md`, C5) — this closes slice-planning.md §3's C1–C5.

Changed:
- `internal/auth/service.go`: new `ServiceCatalog` port (`ServiceExists(ctx, code) (bool, error)`, declared in auth — the consumer, per CLAUDE.md); `Service` gained a `catalog` field, `NewService` a `catalog` parameter (nil is valid: `cmd/emsim/bootstrap.go`'s `Service` never sets a `service_code`, so it wires nil rather than importing `content`).
- `internal/auth/admin.go`: `checkServiceCode` — `CreateUser` and `UpdateUser` (only when a patch actually assigns a non-empty code, never on clear-to-NULL) now check the code against the real catalog: unknown → `*ValidationError{service_code, unknown}` (422 via the existing `writeUserMutationError` mapping, no HTTP-layer change needed); a catalog read failure → `ErrStorage` (500, not masked as a validation failure, per the plan).
- `internal/content/read.go`: `Service.ServiceExists` — content's side of the port; content still never imports auth.
- `cmd/emsim/api.go`: `content.Service` is now constructed before `auth.Service` and passed as its `ServiceCatalog` (content has no dependency on auth, so this ordering is the only one that avoids a forward reference).
- `internal/content/store.go`/`postgres/store.go`: `VersionSummary` gained `Difficulty` (was missing from `openapi.yaml`'s `ScenarioVersionSummary`, caught while touching this area — see also the C4 entry above, this is the second half of the same gap since the SQL wasn't selecting it either).
- `web/src/api/content.ts` (new): `useServices()` (`GET /services`). `web/src/routes/admin/Users.tsx`: the service_code field is now a `<select>` populated from it, not free text; the current value is kept selectable even if absent from the fetched list (a stale/unknown code shouldn't silently vanish from the form).

Validation:
- `go build ./...`, `go vet ./...`, `gofmt -l .` — clean.
- `go test ./...` — all green, including new `internal/auth` tests (`CreateUser`/`UpdateUser` reject an unknown code, accept a known one, surface a catalog read failure as `ErrStorage` not a validation error, clearing `service_code` to NULL never calls the catalog).
- `make verify` (staticcheck included) — passed.
- `go test -tags=integration -count=1 ./test/integration/...` — all green; `TestAPIProcessContentCatalogAccess` now also asserts creating a trainee with an unknown `service_code` gets a real 422 from the running `api` process (not just a unit test).
- `python3 design-docs/contracts/check.py`, `cd web && npm run check`, `npm run lint`, `npm run build` — all clean.
- Manual UI check in the browser against a throwaway PostgreSQL container (migrate → bootstrap-admin → import seed → api serving the freshly built SPA): logged in as admin, opened "Создать пользователя", the "Служба" field is a real `<select>` listing the three seeded services, created a trainee with `service_code=dds_district` picked from it — appears correctly in the user list.

Open issues / risks:
- None new. Slice 2's C1–C5 (slice-planning.md §3's core DoD: import, catalogue read API, auth linkage) are done; C6 (instructor scenario-catalogue UI) and C7 (compose/docs/final acceptance pass) remain, per `slice-2-plan.md`.

Next:
- C6 — `web/src/routes/instructor/` catalogue list + detail view (IncidentCard, Эталон, События, Звонки, Версии), reusing `CardPreview`/`ScenarioReference`.

## 2026-09-19T23:36:52+03:00 — Claude — slice 2: fix two review findings before C6

Status: completed
Slice: 2 (`slice-2-plan.md`) — pre-C6 fixes, found by review of the C1–C5 commits.

Changed:
- `internal/content/import.go`: `ImportScenarios` now computes each file's stored body as `Canonical(bodyRaw(raw))` (the exact bytes `Digest` hashes) instead of letting `postgres.Store.InsertScenarioVersion` re-marshal the typed `Body` struct. The typed struct has no `omitempty`, so a file's legitimately omitted optional field (`hints`, `generation`, `reference.scoring`, ...) was round-tripping into an explicit `null` — the stored `body` no longer matched `digest`, and a served body with those phantom nulls could fail `scenario.schema.json` (`type: array`/`object` rejects `null`). `ScenarioVersionRecord`/`ScenarioDetail` (`store.go`) gained a `BodyJSON []byte` field carrying these canonical bytes end to end; `postgres/store.go`'s `InsertScenarioVersion` writes `v.BodyJSON` verbatim and `scanScenarioVersion` now also returns the raw column bytes on read; `internal/content/http`'s `scenarioJSON.Body` is `json.RawMessage(d.BodyJSON)` instead of a re-marshaled `content.Body`, so `GET /scenarios/{id}` serves the same bytes the digest was computed from (Postgres reformats jsonb on output — spacing differs from `Canonical`'s compact form — so exact reproduction is verified by decoding the stored bytes and re-running `content.Canonical`/`content.Digest`, not by raw byte comparison).
- `internal/content/import.go`: `importOneScenarioVersion`'s version-rule switch merged the `version == maxVersion` and `version < maxVersion` branches into one `version <= maxVersion` case that compares the file's digest against whatever is already stored at that version number (approved or superseded) and no-ops on a match. Previously any `version < maxVersion` was unconditionally `version_regression`, so replaying a full seed directory (including an older file whose version a later import had since superseded) started failing the moment a second version existed — breaking idempotent reseeding (`docker compose up` / `emsim import seed` re-runs) as soon as any scenario got a v2.
- `internal/content/http/handlers_test.go`: `TestScenarioDetailReturnsFullBody`'s fixture now sets `BodyJSON` (via `json.Marshal` of the same `content.Body`, standing in for what a real DB read populates) since the handler no longer reads `Body` for the wire response.

Validation:
- `go build ./...`, `go vet ./...`, `go vet -tags=integration ./...` — clean.
- `go test ./...` and `make verify` (staticcheck included) — all green.
- `go test -tags=integration -count=1 ./test/integration/...` — all green, including two new assertions in `content_import_test.go`: (1) `TestContentImportSeedEndToEnd` decodes the stored `pilot-tree-02` body and checks `content.Digest` of it reproduces the stored digest, and that the stored body has no `"hints"`/`"generation"` keys; (2) `TestContentImportScenarioVersionRules` re-imports a full `{v1, v2}` batch after v2 has superseded v1 and asserts it succeeds with `Unchanged=2` and both versions' status unchanged (this reproduces the exact regression: it failed with `version_regression` before this fix, confirmed locally before applying the fix).
- `python3 design-docs/contracts/check.py` — unaffected, clean (no contract files touched).

Open issues / risks:
- None new. C1–C5 stand as before; these were correctness fixes within already-committed scope, not new functionality.

Next:
- C6 — `web/src/routes/instructor/` catalogue list + detail view (IncidentCard, Эталон, События, Звонки, Версии), reusing `CardPreview`/`ScenarioReference`.

## 2026-09-19T23:47:24+03:00 — Claude — slice 2 C6 (instructor scenario catalogue UI)

Status: completed
Slice: 2 (`slice-2-plan.md`, C6) — instructor-facing catalogue UI on top of C4's read API; no backend changes.

Changed:
- `web/src/api/content.ts`: extended with `Scenario`/`ScenarioSummary`/`ScenarioVersionSummary`/`CardPreview`/`ScenarioReference`/`ContactPreview` types and `useScenarios(filter)` (GET /scenarios, service/status/difficulty_min/max/page/page_size), `useScenario(id)`, `useScenarioVersions(id)`, `useScenarioPreview(id)`.
- `web/src/format.ts` (new): `formatDateTime`, `formatOffset` (renders `registered_at_offset_s` as "За N с до выдачи" per the plan).
- `web/src/labels.ts` (new): Russian labels for `ReactionStatus`, applicant status, scenario/version status — shared so `IncidentCard` and the detail route don't duplicate the maps.
- `web/src/components/IncidentCard.tsx` (new): renders `CardPreview` (number/time, applicant, address, incident+features, notification list, phones, contacts) — pure presentational, no action controls, explicitly meant for slice 3 to reuse for the trainee's live card around the same component.
- `web/src/routes/instructor/ScenarioCatalogue.tsx` (new): `/instructor/scenarios` — service/status/difficulty filters (status defaults to `approved`), table, pagination, loading/empty/error states (mirrors `admin/Users.tsx`'s shape).
- `web/src/routes/instructor/ScenarioDetail.tsx` (new): `/instructor/scenarios/:scenarioId` — scenario meta, `IncidentCard`, and four blocks: Эталон (primary decision, field corrections rendered as "current → expected" using the preview card's own address, the pilot_goal notice, guide refs/notes), События (`has_events` from the scenario summary), Звонки (`reference.call`, resolving `call.to` against the preview's contacts), Версии (`/versions`, digest truncated). No JSON view, no edit/approve controls, per the plan.
- `web/src/App.tsx`: new `RequireAuth roles={["instructor"]}` route group for the two paths above. `web/src/components/Layout.tsx`: instructor nav link. `web/src/routes/Home.tsx`: instructor home now links to the catalogue. `web/src/api/errors.ts`: field labels for `difficulty_min`/`difficulty_max`. `web/src/index.css`: minimal styles for the filter form, the incident card, and generic `dl/dt/dd` layout (was unstyled everywhere, including the existing trainee home screen).

Validation:
- `cd web && npm run check` (tsc), `npm run lint` (oxlint), `npm run build` — all clean; `npm run generate:api` confirmed the OpenAPI contract needed no changes (C4 already added every schema this UI uses).
- `make verify` (staticcheck included) — clean; unaffected since no Go files changed.
- Manual browser walkthrough against a throwaway PostgreSQL container + the freshly built binary (migrate → bootstrap-admin → import seed → api, an instructor and an admin user created via the admin API): catalogue lists both pilot scenarios with the approved-status default filter; pilot-tree-01's detail shows "Первичное решение: Принять" with no "Исправления" section; pilot-tree-02 shows the ЮАР→ЮАО correction, the "Пилот: цель — принятие карточки..." notice, "Событий нет.", "Не требуются.", and a one-row Версии table; an admin navigating to `/instructor/scenarios` directly is redirected to their own home (RequireAuth's existing role guard); an unknown scenario id resolves to "Не найдено." (correct, just slow — TanStack Query's default 3-retry backoff on the 404, a pre-existing app-wide default this change did not introduce or need to override).

Open issues / risks:
- None new. This closes slice-planning.md §3's C1–C6. Only C7 (compose/seed wiring, README, final acceptance pass) remains for slice 2.

Next:
- C7 — Dockerfile/.dockerignore for `design-docs/contracts` + `seed/`, compose one-shot `seed` service, `make seed`, README update, final `make verify`/`make test-integration`/`docker compose up --build` acceptance pass.

## 2026-09-19T23:58:06+03:00 — Claude — slice 2 C7 (seed wiring, docs, acceptance)

Status: completed
Slice: 2 (`slice-2-plan.md`, C7) — this closes slice-planning.md §3's full C1–C7 DoD.

Changed:
- `Dockerfile`: build stage now `COPY design-docs/contracts design-docs/contracts` — this was missing entirely before, so `go build` inside the container was broken (`internal/content/schema` imports `emsim/design-docs/contracts`, C1's `//go:embed`, and the package didn't exist in the build context). Runtime stage now `COPY seed /app/seed` so the `import seed` compose service has something to read.
- `.dockerignore`: `design-docs` stays blanket-excluded, but `!design-docs/contracts/*.json` and `!design-docs/contracts/embed.go` let the embedded schemas and the embed source through (check.py/schema.sql/openapi.yaml's own consumer stays the web-build stage's existing narrower COPY). `seed/` was never excluded, so no change needed there.
- `compose.yaml`: new one-shot `seed` service (`import seed --actor ${BOOTSTRAP_ADMIN_LOGIN:-admin} /app/seed`), depends on `bootstrap` completing (import's actor must be an active admin); `api` now depends on `seed` completing instead of `bootstrap` directly, so the SPA never comes up in front of an empty catalogue.
- `Makefile`: new `seed` target (`docker compose run --rm seed`) for re-seeding after editing `seed/` without restarting the rest of the stack.
- `README.md`: documented the new `seed` compose step and its idempotency, added the instructor catalogue to the demo walkthrough, pointed the "upgrading an existing install" question at `seed/README.md` (already written during C3 and still accurate — no change needed there), added `make seed`/`python3 design-docs/contracts/check.py`/`npm run lint` to the dev command list, and described the C6-era addition to `api_process_test.go`'s second process test.

Validation:
- `docker build -t emsim:local .` — clean; confirmed by inspecting the built image that `/app/seed/**` and the embedded schema/rubric JSON (via a successful `go build`, which would fail immediately without design-docs/contracts present) are actually there, not just that the Dockerfile parses.
- `docker compose up --build` against a clean volume: `migrate` → `bootstrap` → `seed` → `api` all completed/started in that order; `seed` logged `services: created=3 unchanged=0`, `classifier: created=1 unchanged=0`, `scenarios: new_scenarios=2 new_versions=2 unchanged=0`; a second `docker compose up --build` (no volume reset) logged `created=0 unchanged=3/1/2` across the board — the reimport-after-supersede fix from the pre-C6 review pass and C3's original idempotency rules both hold under the real compose flow, not just `go test`.
- Manual acceptance pass against that running stack, per `slice-2-plan.md`'s C7 verification list: instructor catalogue with the service filter (narrowing to a service that is only in a scenario's notification list, not its target, correctly shows "Сценарии не найдены"); `POST /admin/users` with an unknown `service_code` → real `422 validation_failed`; a trainee hitting `/instructor/scenarios` in the browser is redirected to their own home, and the same request against `GET /scenarios` over the API → real `403 forbidden`.
- `make verify`, `make test-integration`, `python3 design-docs/contracts/check.py`, `make verify-web`, `cd web && npm run lint`, `make compose-config` — all clean (run after the Docker acceptance pass, from a clean `web/dist` — no build artifacts left staged).
- Did not separately re-drive the "existing slice-1 install with an orphan `service_code`" upgrade path by hand through Docker: it is already covered end to end by `test/integration/content_fk_test.go` (migrates to schema v3, inserts the orphan row, migrates to v4, then exercises the same orphan-report → fix → validate sequence `seed/README.md` documents), which passed as part of `make test-integration` above.

Open issues / risks:
- None new. Slice 2 (`slice-planning.md` §3, C1–C7) is complete: contracts/migration, content domain, storage/import/CLI/seed, instructor HTTP API, auth-content linkage, instructor catalogue UI, and now container/compose wiring plus documentation.

Next:
- Slice 3 (`slice-planning.md` §4) — the first end-to-end single-card run: lesson draft, one assignment, start/run/item creation, the `training/dds` command surface, and the trainee's first working card screen.

## 2026-09-20T09:33:43+03:00 — Claude — slice 2: post-review fixes (7 findings) verified

Status: completed (uncommitted in the worktree at the time of writing)
Slice: 2 — fixes for an external review of the C1–C7 commits; no new functionality. The fixes were applied by the user; this entry records the verification pass and the residual notes.

Changed:
- `internal/auth/admin.go`: `UpdateUser` runs `checkServiceCode` before `store.WithTx` — the catalog lookup owns its own transaction, so running it inside the update transaction needed a second pool connection (hard deadlock at `pool_max_conns=1`). Side effect: an unknown `service_code` on a non-existent user now yields 422 before the 404.
- `migrations/00005_scenario_version_lifecycle.sql` (new) + `design-docs/contracts/schema.sql`: `protect_scenario_version_content` now also rejects backward status transitions (only draft→approved|superseded, approved→superseded) and any change to `approved_by/approved_at` outside a draft→approved step. This closes the delete bypass (rewind to draft, then DELETE). `platform/postgres.ExpectedSchemaVersion` = 5.
- `internal/content/digest.go`: `Canonical` normalizes JSON numbers to PostgreSQL jsonb's exact-decimal rendering (`1e2`→`100`, `1.230e-5`→`0.00001230`, scale preserved) on strings, without float64; unstorable exponents are left as-is so the INSERT reports the storage error.
- `internal/content/http/handlers.go`: `/scenarios/{id}/preview` serializes `reference` through a dedicated `referencePreviewJSON` DTO — absent optional fields are omitted instead of `null`/`""`, `expected_chain` is always an array.
- `internal/content/validate.go` + `store.go` + `postgres/store.go` + `import.go`: `Catalog.ScenarioVersion(id)` and `Store.VersionReferenceByID`; `spawn_card` with `kind=scenario` is now validated for existence, published status (approved/superseded with attribution), matching `exercise_type` and `target_service`.
- `internal/content/import.go`: decoded files are sorted by `(scenario key, numeric version, filename)` before application — a full history no longer depends on filename order (`v10` before `v2`).
- `test/integration/harness_test.go`: `resetSchema` drops the content tables and the two trigger functions in dependency order (fixes `SQLSTATE 2BP01` on `TEST_DATABASE_URL` runs).

Validation:
- `gofmt -l` clean; `make verify` (format-check/build/test/vet/staticcheck) exit 0; `make test-integration` ok (~49 s) — includes new tests: single-connection `UpdateUser`, lifecycle rewind/reattribution rejected after migrating 4→5, `Canonical()` vs `::jsonb::text` on real PostgreSQL for six literals, unknown/compatible `spawn_card` references, ten-version misordered history, `resetSchema` round-trip.
- `python3 design-docs/contracts/check.py` with `DATABASE_URL` against a throwaway `postgres:16-alpine` — DDL applied, all invariant checks passed.

Open issues / risks:
- `spawn_card.scenario_version_id` is a UUID assigned by `uuid.New()` at import, so seed files cannot reference another version reproducibly across installs; the new validation correctly rejects such files. Slice 4 must decide on deterministic version IDs or a `(source_key, version)` reference form before `spawn_card` scenarios can be shipped as seed data.
- `storeCatalog` maps storage errors to "unknown"/"not found" (pre-existing pattern, now also for `ScenarioVersion`); a transient DB error during import surfaces as a validation error rather than `ErrStorage`.
- Untracked `c.txt` (curl cookie jar) is present in the worktree; left untouched per user instruction — must not be committed.

Next:
- Commit these fixes, then slice 3 (`slice-planning.md` §4): lesson draft, single assignment, start/run/item, `training/dds` command surface, trainee card screen.

## 2026-09-20T10:28:53+03:00 — Claude — slice 3 C0–C3 (contracts, migration, DDS domain rules)

Status: in progress. C0–C3 of `slice-3-plan.md` committed; C4 (transactional lessons/commands service) next.

Changed:
- C0 (`fix: finalize slice 2 review corrections`): committed the 19 files + `migrations/00005_scenario_version_lifecycle.sql` that were sitting uncommitted in the worktree at session start (already validated per the 2026-09-20T09:33 entry below). `c.txt` (a curl cookie jar) was left untracked, per prior instruction.
- C1 (`docs: define slice 3 pilot rules and contracts`): new `design-docs/adr/017-slice3-pilot-close-and-field-correction.md` — pilot `close` from `accepted` (`close_reason=pilot_completed`) for `reference.pilot_goal=accept_card`, and `set_card_field` restricted to `/card/address/okrug`. `rfc-001-emsim.md` §6/§7.4 and `slice-planning.md` §4 reference it without changing the general close/actions invariants. New `slice-3-plan.md` records the accepted commit-by-commit plan. `contracts/openapi.yaml`: `Assignment.user_id` required; `Command` gains `set_card_field`; `Action.effect`; `close_reason` gains `pilot_completed`; `Item.mode`/`allowed_transitions`; `CardView` expanded to the full public projection with absolute `registered_at`; `MyRun.mode`/`workstation_no`; new `GET /lessons/options`; stop/monitor/stream/recording endpoints annotated as not implemented until slice 4/5. `contracts/schema.sql`: `assignments.user_id` NOT NULL; `runs.mode` + partial unique indexes for one active run per user/workstation; `items.card`/`workflow`/`pilot_goal`; `actions.effect` and `set_card_field` in the type CHECK. `contracts/evidence.schema.json`/`.example.json`: `mode`, `final_card`, `actions[].effect`, `pilot_completed`. `contracts/check.py`/`check-invariants.sql`: positive/negative `set_card_field` examples, a `pilot_completed` evidence example, fixtures updated for the new NOT NULL columns.
- C2 (`training: add lesson run item and evidence storage`): `migrations/00006_training_lessons_runs_items.sql` — `lessons`, `assignments`, `runs`, `items`, `actions`, `evidence`, 1:1 with the amended `schema.sql`; `reject_immutable_change()` applied to `evidence`/`actions`. `internal/platform/postgres/postgres.go`: `ExpectedSchemaVersion=6`, `applicationTables` extended. `test/integration/harness_test.go`: `resetSchema`/`assertTableSet`/`assertApplicationTablesAbsent` updated. New `test/integration/training_schema_test.go`: append-only `actions`/`evidence` (UPDATE and DELETE both rejected), the one-active-run-per-user/workstation partial unique indexes across two lessons, users/content-catalogue untouched.
- C3 (`training: implement DDS rules and evidence projection`): new `internal/training` (domain: `Item`/`Command`/`Decision`/`Rejection`/`RequestDigest`/`EvidenceBody`/`SealEvidence`) and `internal/training/dds` (the `exercise_type=dds_processing` `Exercise` implementation — `open`/`set_status`/`add_comment`/`set_card_field`/`close`; `call_start`/`call_end`/`control_report` rejected as `transition_not_allowed`, not implemented until slices 4/5). No HTTP or PostgreSQL in either package (CLAUDE.md's hexagonal boundary); the application service that wires this to the database is C4. `internal/content/preview.go` gained `json` tags on `CardPreview` and its nested types (previously untagged) so `training` can marshal an item's card/`evidence.final_card` into a stable snake_case shape.

Validation:
- `python3 design-docs/contracts/check.py`, including the DDL section against a throwaway `postgres:16-alpine` container (schema.sql applies cleanly, all SQL invariants pass) — done during C1.
- `cd web && npm run generate:api && npm run check && npm run build` — clean (C1; the OpenAPI additions type-check against the existing, unmodified web code).
- `go build ./...`, `go vet ./...`, `make verify` (staticcheck included) — clean after each of C1–C3.
- `go test -tags=integration -count=1 ./test/integration/...` — all green after C2 (new `TestTrainingSchemaConstraints`) and again after C3 (confirms the `preview.go` json-tag addition changed nothing observable).
- `go test ./internal/training/... -v` — 27 tests green, including both pilot scenarios end to end through `Exercise.Evidence`, with the sealed evidence body validated against `design-docs/contracts/evidence.schema.json` compiled directly from disk (test-only; nothing in the shipped binary validates evidence against the schema at runtime — the Go struct tags are what keep the shape correct).

Open issues / risks:
- `content.Store.VersionByID` (a full `ScenarioVersionRecord` read by version id — today only `VersionReferenceByID`'s narrower projection exists) is still needed for C4's `ScenarioReader` port; not added yet.
- C4 must wire cross-module reads (`content`/`auth`) through ports that accept the caller's own `pgx.Tx` (adapters over `authpg.Store`/`contentpg.Store`, not `*auth.Service`/`*content.Service`) to avoid the `pool_max_conns=1` deadlock noted in the 2026-09-20T09:33 entry.
- `GroupItemRead` (a role group covering both instructor-with-reference and trainee-without-reference reads of `GET /items/{id}`) does not exist yet in `internal/auth/authz.go`; needed by C5.

Next:
- C4 — `internal/training/service.go` (CreateLesson/ReplaceAssignments/Start/Execute/reads) and `internal/training/postgres/store.go`, per `slice-3-plan.md`'s C4 section: lock ordering, replay-before-state-check (ADR-004 §7.1), evidence insert atomic with item/run/lesson close.

## 2026-09-20T10:59:12+03:00 — Claude — slice 3 C4 (transactional lessons/commands service)

Status: in progress. C0–C4 of `slice-3-plan.md` committed; C5 (HTTP layer) next.

Changed:
- `internal/training/service.go`: the application layer — `CreateLesson`, `ReplaceAssignments`, `Start`, `Execute`, and reads (`MyRun`/`MyItems`/`ItemForTrainee`/`ItemForInstructor`/`RunActions`/`ListLessons`/`Lesson`/`LessonOptions`). `Execute` implements ADR-004 §7.1's full protocol: ownership/workstation check before any lock, lock order lessons→runs (close only)→items, replay lookup before running/closed/expected_seq, the three generic rejections decided here (not by the exercise), and one atomic transaction for the action insert + item update + (on close) evidence insert + run finish.
- `internal/training/ports.go`: `UserDirectory`/`WorkstationDirectory`/`ScenarioReader`/`ServiceReader`, each with method signatures copied verbatim from existing `*authpg.Store`/`*contentpg.Store` methods — those concrete types satisfy these ports structurally, no adapter type needed. This is also why every port method takes the caller's own `pgx.Tx` rather than going through `*auth.Service`/`*content.Service` (see the 2026-09-20T09:33 entry's `pool_max_conns=1` deadlock).
- `internal/training/postgres/store.go`: `training.Store` over pgx — lessons/assignments/runs/items/actions/evidence.
- `internal/content`: `Store.VersionByID` (full `ScenarioVersionRecord` by version id) and `content.RubricVersion()`.
- `internal/training/domain.go`/`exercise.go`/`lesson.go`/`receipt.go`/`store.go`: `Lesson`/`Assignment`/`Run`/`ItemPatch`/`Receipt` domain types; json tags added to `Timing`/`Deadlines` for JSONB storage; `Action` extended with `ItemID`/`RequestDigest`/`CommandID`/`Receipt`/`HTTPStatus`/`ClientAt` for persistence/replay (C3 only needed it for evidence assembly).

A real bug found by this commit's own concurrent-command integration test: `recordDecision` originally only called `ApplyItemDecision` (which persists `items.log_seq`) when a decision was **accepted** — a rejected attempt's `log_seq` increment stayed in memory and was never written back, so the next command (either outcome) recomputed the same `log_seq` and collided with `actions`' `UNIQUE(item_id, log_seq)`. Fixed: `ApplyItemDecision` now always runs; only the close-specific `closed_at`/`close_reason` fields are conditional on `decision.Accepted && decision.Close != nil`.

Validation:
- `go build ./...`, `go vet ./...`, `make verify` (staticcheck included) — clean.
- `go test -tags=integration -count=1 ./test/integration/...` — all green, twice in a row (to rule out the log_seq bug as a flake once fixed). New `test/integration/training_service_test.go`: full pilot flow end to end (evidence row, run finished, user/workstation freed); two independent assignments sharing one scenario version; concurrent commands on one item (one applied, one `stale_seq`); replay of an applied and a rejected action, a different body under the same `command_id` (`command_id_conflict`), replay after close; concurrent `Start` on one lesson (idempotent, no duplicate run); transaction rollback when evidence assembly fails (no partial action/item/run state, retry succeeds).
- One `TestTrainingSchemaConstraints` run hit a transient testcontainers/Docker reaper failure (port-mapping race, unrelated to this change) and passed cleanly on retry.

Open issues / risks:
- `GroupItemRead` (a role group covering both instructor-with-reference and trainee-without-reference reads of `GET /items/{id}`) still does not exist in `internal/auth/authz.go`; needed by C5.
- `Service.Execute`'s lock-acquisition (`lockForCommand`) does two unlocked "peek" reads (item, then run) before acquiring the real locks in RFC order — correct because `items.run_id`/`runs.lesson_id`/`runs.user_id`/`runs.workstation_id` never change after insert, but worth a reviewer's second look if a future slice ever makes any of those columns mutable.
- `internal/training/service.go`'s `startOneAssignment` re-validates the scenario version's compatibility at start (in addition to `ReplaceAssignments`' own check) — intentional (RFC-001: assignment *and* start both check compatibility) but means content changes between assign and start are re-checked, not just approval status.

Next:
- C5 — `internal/training/http/handlers.go` (instructor lesson routes + `GET /lessons/options`; trainee `/my/run`, `/my/items`, `POST /items/{id}/actions`; `GET /items/{id}` under a new `GroupItemRead`), wired into `cmd/emsim/api.go` alongside the `*authpg.Store`/`*contentpg.Store`-backed `training.NewService(...)` composition this commit's own tests already exercise.

## 2026-09-20T16:20:00+03:00 — Claude — slice 4 C6 (restart recovery + scheduler wiring)

Status: completed. Resuming `slice-4-plan.md`/ADR-018 at C6 — C1–C5 (contracts, portable spawn references, event/control-report storage, group assignment queues, the event/hard-offer scheduler) were already committed (`e3d0714`…`f106b2c`) by a prior session but never got a LOG.MD entry; this entry does not redo that work, only records what changed from here.

Changed:
- `internal/training/store.go`/`postgres/store.go`: `RecoverOpenItems(tx, Interruption)` — one `UPDATE ... FROM runs, lessons` that appends one interruption marker (jsonb concat) to every open item (`offered`/`opened`/`in_progress`) of a `running` lesson, guarded by `NOT EXISTS (... elem->>'recovery_id' = $2)` so a repeated call with the same `recovery_id` is a no-op; `offered_at`/`deadlines`/`due_at` are never touched. `SkipRemainingItemEvents(tx, itemID, reason)` — bulk-cancels an item's still-`scheduled` events.
- `internal/training/service.go`: `Service.Recover(ctx, recoveryID, cause)` wraps `RecoverOpenItems` in one transaction plus an audit row (`training.recover`, system actor). `recordDecision`'s close branch now calls `SkipRemainingItemEvents` (reason `item_closed`, new `domain.go` constant, also reused by `tickEvent`'s own lazy fallback) and passes the item's `item_events` rows into `exercise.Evidence` — RFC-001 §7.4's close pseudocode ("отменить scheduled" before the snapshot) was not implemented by C5 despite being in its own plan bullet ("Включить состояния событий в evidence"); fixed here since evidence completeness is this commit's own concern anyway.
- `internal/training/exercise.go`/`dds/evidence.go`: `Exercise.Evidence` gained an `events []ItemEvent` parameter; `dds`'s implementation projects it into `evidence.schema.json`'s `events[]` shape and the item's own `Interruptions` into `interruptions[]` (both were hardcoded `[]any{}` placeholders since slice 3).
- `internal/training/evidence.go`: `EvidenceBody.Events`/`.Interruptions` typed as `[]EvidenceEvent`/`[]Interruption` instead of `[]any`.
- `internal/training/delivered_event.go` (new): `DeliveredEvent` + `Service.deliveredEventsForItem` — resolves an item's *delivered* `item_events` against its scenario version's event definitions (delivery/from/text/voice); scheduled/skipped events are never surfaced to a trainee (RFC-001 §7.2: "expects обучаемому не раскрывать"). Wired into `ItemForTrainee`/`ItemForInstructor` (both gained a `[]DeliveredEvent` return) and `internal/training/http/handlers.go`'s `GET /items/{id}` response (`events[]`, `interruptions[]` — previously always empty/`[]any{}`).
- `cmd/emsim/api.go`: `newPublicHTTP` now also returns the composed `*training.Service`; `runAPI` calls `Recover(ctx, uuid.New(), "server_restart")` once, synchronously, before `metrics.SetReady("api", true)` — no request is ever accepted before recovery commits — then starts a 500ms `runTrainingScheduler` goroutine calling `Service.Tick` (already built by C5, never wired to anything until now). A single `Tick` error is logged (`observability.Logger.Operation`, not raw `slog` — keeps the codebase's one whitelisted-log-shape rule) and retried next tick; an unrecovered panic is deliberately left to crash the process, since RFC-001 §7.2's "a scheduler crash makes the process unhealthy and leads to a restart" is exactly what Go's own panic-crashes-the-binary-then-docker-restarts-it semantics already give for free — no recover() was added.
- `test/integration/content_import_test.go`: unrelated pre-existing break found while running `make test-integration` — C2 (`0236588`) added `seed/scenarios/pilot-z-events-01.json` (the cross-scenario `spawn_card` seed) but never updated `TestContentImportSeedEndToEnd`'s hardcoded `NewScenarios/NewVersions/Unchanged`/`ListScenarios` counts (2→3). Fixed the three assertions; confirmed on `git stash` that the failure predates this session's changes.

Validation:
- `go build ./...`, `go vet ./...`, `gofmt -l .` (repo, minus `orchestration-core-main`), `make verify` — clean.
- `go test -tags=integration -count=1 ./test/integration/...` — all green, including new `TestTrainingRecoverMarksOpenItemsIdempotently` (marks the one open item, repeat call with the same `recovery_id` affects nothing, `offered_at`/`deadlines` byte-identical before/after, a second distinct `recovery_id` adds a second marker, both markers present in evidence at close) and the new `TestEvidenceProjectsEventsAndInterruptions` unit test (`internal/training/dds`) validating a delivered+skipped event and an interruption marker against `evidence.schema.json`.

Open issues / risks:
- The recovery step and the scheduler goroutine both run against the same `*training.Service`/pool `newPublicHTTP` already builds for HTTP — fine at `pool_max_conns` sized for one API process, but worth remembering if a future slice ever runs more than one API process (RFC-001 §7.2 explicitly scopes recovery to exactly one process for this MVP).
- `runTrainingScheduler`'s "log and continue" policy means a *persistent* (not panicking) `Tick` failure — e.g. the DB is down — never flips readiness by itself; `apiSchemaReady`'s own `/readyz` check already covers a down DB independently, so this is not a gap, just worth a reviewer's note.
- Voice rendering (`DeliveredEvent.voice_url`) is always `null` — no `voice_assets`/Piper pipeline exists yet (later slice); the field is wired through so the contract shape is already correct.

Next:
- C7 — `training: persist post-close control reports`: dedicated `control_report` branch in `Service.Execute` (before the generic running/closed checks — opposite of every other command), `control_reports` row, still no evidence/reaction change.

## 2026-09-20T16:45:00+03:00 — Claude — slice 4 C7 (post-close control reports)

Status: completed.

Changed:
- `internal/training/service.go`: `Execute` gained a `cmd.Type == CommandControlReport` branch, checked right after replay lookup and before the generic `lesson.State != LessonRunning`/`item.State == Closed`/`stale_seq` switch — the opposite polarity of every other command (RFC-001 §7.5: allowed only *after* close/interrupt, and explicitly still allowed once the lesson itself has stopped/finished, since the branch never checks `lesson.State`). New `recordControlReport`: validates `item.State` (closed/interrupted → else `transition_not_allowed`), `expected_seq` (→ `stale_seq`), and a non-blank `{text}` payload (→ `invalid_payload`); on success inserts the `actions` row and a `control_reports` row in the same transaction, and calls `ApplyItemDecision` with the item's own unchanged `reaction`/`state`/`card` (log_seq/seq still advance, per RFC-001 §6). Deliberately does not reuse `recordDecision`: that function's close/event-scheduling logic would re-run `scheduleEventsForAnchor` for the item's already-final `reaction` and collide with `item_events`' `UNIQUE(item_id, event_key)` (those events are terminal since C6 skips them at close) — a plain, separate persistence path avoids that bug entirely rather than special-casing it inline.
- No HTTP/contract changes: `control_report` already flowed through the existing generic `POST /items/{id}/actions` (added to the OpenAPI `Command` oneOf back in slice 3's C1); it now simply stops being rejected as `transition_not_allowed` by `internal/training/dds`'s exercise (which never sees it — the branch runs before `exerciseFor` is even called).

Validation:
- `go build ./...`, `go vet ./...`, `gofmt -l .`, `make verify` — clean.
- `go test -tags=integration -count=1 ./test/integration/...` — all green, including new `TestTrainingControlReportAfterClose`: rejected before close (`transition_not_allowed`), accepted after close (seq/log_seq advance, `reaction`/evidence byte-identical before and after), the `control_reports` row exists and is linked to the right `action_id`, and a replay of the same `command_id` returns the original receipt with `replayed=true` and does not insert a second `control_reports` row.

Open issues / risks: none new.

Next:
- C8 — `training: stop lessons through durable close task`: `POST /lessons/{id}/stop` under `lessons FOR UPDATE` (barrier: `stopped_at`, `stop_reason`, epoch+1, `stop_cutoff_log_seq` on every open item, skip their scheduled events, enqueue `lesson.close:<id>:<epoch>`), plus a new `platform/tasks` handler that closes the remaining items as `interrupted` with `EvidenceInterruption` (the singular field C6 left `nil`), finishes runs/lesson, and is itself fenced/idempotent per `internal/platform/tasks`' existing at-least-once guarantees.

## 2026-09-20T17:05:00+03:00 — Claude — slice 4 C8 (stop through a durable close task)

Status: completed.

Changed:
- `internal/training/ports.go`: new `TaskEnqueuer` port (`EnqueueTx(ctx, tx, tasks.EnqueueRequest)`), matching `*tasks.Store` structurally — the same consumer-owned-port pattern as `UserDirectory`/`ScenarioReader`. `Service` gained a `tasks TaskEnqueuer` field; `NewService`'s signature grew a parameter (updated every call site: `cmd/emsim/api.go`, the new `cmd/emsim/training_composition.go`, `test/integration/training_service_test.go`).
- `internal/training/service.go`: `Service.Stop` — under the same `lessons FOR UPDATE` lock `Start` uses, freezes `stopped_at`/`stop_reason`/`epoch+1`, freezes every open item's `stop_cutoff_log_seq` (new store method `SetStopCutoffForOpenItems`, one bulk `UPDATE ... FROM runs WHERE ... state IN ('offered','opened','in_progress') AND stop_cutoff_log_seq IS NULL`), cancels their remaining scheduled events (reusing C6's `SkipRemainingItemEvents`, reason `lesson_stopped`), audits, and atomically enqueues `training.KindLessonClose` (dedup_key `lesson.close:<lesson_id>:<epoch>`) — all in one transaction, so the barrier and the task that will act on it can never disagree. A repeat call on an already-stopped/finished lesson returns the current row unchanged (no new epoch/task); a call on a draft lesson is `ErrConflict`. `Service.CloseStoppedLesson(ctx, tx, lessonID)` — the task's domain half, taking the *caller's* transaction (not opening its own) so `cmd/emsim`'s handler can commit it atomically with `tasks.Terminal`: locks the lesson, interrupts every still-open item at the lesson's own `stopped_at` (never `clock_timestamp()` — the worker's delay must not count against the trainee), seals evidence via `closeInterruptedItem` (no new action row — this is a server effect, not a client command), finishes each run once its items are terminal, finishes the lesson once its runs are. Idempotent by construction: every write is a conditional `UPDATE`/state-guarded, so a retried task attempt safely no-ops on what's already done.
- `internal/training/postgres/store.go`: `StopLesson`, `SetStopCutoffForOpenItems`. **Bug fixed in the same commit**: `FinishLesson`'s `WHERE state = 'running'` only ever matched slice 3's natural-exhaustion path; `CloseStoppedLesson` calls it on an already-`stopped` lesson, which matched 0 rows and returned `ErrNotFound` — now `WHERE state IN ('running', 'stopped')`. Found by the new integration test hanging indefinitely: a `t.Fatalf` inside a hand-rolled `pool.Begin()`/`Commit()` block (no `defer rollback`) left the connection `idle in transaction` forever once `CloseStoppedLesson` started returning that error, which is also why the test now defers `tx.Rollback` around both manual transactions as a safety net.
- `internal/training/dds/evidence.go`: `buildEvidenceInterruption` — the singular `interruption` field (distinct from C6's `interruptions` array) is now populated for `close_reason=interrupted`: `{reason:"stop", stopped_at, unreached_events}`, where `unreached_events` lists the keys of this item's own events skipped with reason `lesson_stopped`.
- `internal/training/http/handlers.go`: `POST /lessons/{lessonId}/stop` — optional `{reason}` body (≤500 chars, validated), delegates to `Service.Stop`.
- `design-docs/contracts/openapi.yaml`: `/lessons/{lessonId}/stop` gained the `409 Conflict` response `/start` already had (was missing).
- `cmd/emsim/training_composition.go` (new): `newTrainingService(pool, taskEnqueuer)` — one composition function shared by `api.go` (its own `*tasks.Store` built purely to enqueue, via new `mustTaskEnqueuer`) and `worker_composition.go` (reuses the pool's real `*tasks.Store`), so neither can drift from the other's `training.KindLessonClose` `Spec`.
- `cmd/emsim/worker_composition.go`: `registerKinds` — the single place both processes get `Spec`s from — gained `training.KindLessonClose` (pool short, priority 100, lease 2m, 5 attempts, retry base 200ms, per `slice-4-plan.md`'s C8 spec); new `lessonCloseHandler` mirrors `noopHandler`'s own hand-rolled-transaction fencing pattern (domain close + `tasks.Terminal` commit together).

Validation:
- `go build ./...`, `go vet ./...`, `gofmt -l .`, `make verify`, `make verify-web` — clean.
- `go test -tags=integration -count=1 ./test/integration/...` — all green, including new `TestTrainingStopBarrierAndDurableClose`: barrier (`stop_cutoff_log_seq` frozen at open+accept's log_seq 2, exactly one `lesson.close` task row with the expected `dedup_key`, a command after stop rejected `lesson_stopped`), repeat `Stop` idempotent (same epoch, no second task), `CloseStoppedLesson` run twice back-to-back (retry is a no-op — same evidence row, `count=1`), item interrupted at `stopped_at` not wall-clock time, run/lesson finished, evidence's singular `interruption` populated, `control_report` still works on the now-interrupted item, and `Stop` on the now-finished lesson stays idempotent. `cmd/emsim`'s own `TestRegisterKindsIncludesLessonClose` (fast, no DB) guards the shared `Spec`.
- Cleaned up two Docker containers orphaned by an earlier killed test run (`docker rm -f`) before the final green run — unrelated to any code change, just session hygiene.

Open issues / risks:
- `lessonCloseHandler`'s fencing relies entirely on `tasks.Terminal`'s own lease-token check (same as `noopHandler`); `CloseStoppedLesson` itself has no fencing awareness (it just re-locks and re-checks domain state), which is correct only because the domain effect and `tasks.Terminal` commit in the same transaction — worth a reviewer's second look if a future slice ever needs `CloseStoppedLesson` reachable from somewhere that isn't gated by that same commit.

Next:
- C9 — `platform: add SSE invalidations and live lesson monitor`: PostgreSQL `LISTEN`/`NOTIFY` (payload is identifiers only, `NOTIFY` inside the domain transaction), a LISTEN-projector with epoch/cursor/1000-entry ring buffer/15s heartbeat/`resync`, `GET /lessons/{id}/stream` and `GET /my/stream`, `GET /lessons/{id}/monitor` snapshot (`active_items[]`, `queue_left`, `done`, `last_action`, best-effort in-process `online` presence). Reorder `cmd/emsim/api.go`'s `runAPI` so LISTEN is established before C6's recovery call, per ADR-018.

## 2026-09-20T17:35:00+03:00 — Claude — slice 4 C9 (SSE invalidations and live monitor)

Status: completed.

Changed:
- `internal/platform/realtime` (new package): `NotifyTx(ctx, tx, Event)` — a plain `pg_notify` call from inside the caller's own domain transaction, not a table write, so no port/module-boundary ceremony is needed to call it from `training`. `Hub` — process-local fan-out: a monotonic (`sync/atomic`, not wall-clock — two epochs minted back to back must never collide) *process epoch* that changes only when `Reset()` runs, a 1000-entry ring buffer, and `Since(cursor)` resolving a client cursor to either the events after it or `resync=true` (malformed cursor, superseded epoch, or a position the buffer already evicted). `RunListener` — one dedicated `pgxpool.Conn` doing `LISTEN emsim_events` in a retry loop, calling `hub.Reset()` before every (re)subscription (ADR-018: resync on "восстановление LISTEN") and signalling a `ready` channel once after its first successful LISTEN.
- `internal/training/service.go`: `notify(ctx, tx, lessonID, userID, itemID)` wraps `realtime.NotifyTx`, called from every place training's own domain state actually changes: `recordDecision` (both accepted and rejected — a rejected attempt still moves `log_seq`, worth the monitor's `last_action` refreshing for), `recordControlReport`, `Stop`, `offerQueueVersion` (covers the normal-close queue advance, hard-tick offers, and `spawn_card` — one call site, since `spawnEventCard` itself calls `offerQueueVersion`), `tickEvent`'s delivery, and `closeInterruptedItem`.
- `internal/training/monitor.go` (new): `Service.Monitor(ctx, actor, lessonID)` — one assignment-owning-a-run per row (`MonitorRow`: `WorkstationNo`, `User`, `RunID`, `ActiveItems` (every non-terminal item, hard can have more than one), `QueueLeft`, `Done`, `LastAction`), read straight from PostgreSQL, never derived from the SSE buffer. New store method `LastActionByRun` (join `actions`→`items` by `run_id`, `ORDER BY server_at DESC LIMIT 1`) backs `LastAction`. `Online` is deliberately left for the HTTP layer to fill in (presence is a connection-lifecycle concern, not a database read).
- `internal/training/http/realtime.go` (new): `presenceTracker` — an in-process ref-counted `map[uuid.UUID]int`, incremented/decremented across exactly one `/my/stream` connection's lifetime (a trainee with two tabs stays online until both close). `GET /lessons/{id}/monitor` (`getMonitor`) and `GET /lessons/{id}/stream` / `GET /my/stream` (`serveSSE`, shared by both, parameterized by a `match(Event) bool` filter — lesson id for the instructor, user id for the trainee): fresh connection → `stream.ready` only (client is expected to then read a snapshot before trusting anything else); a reconnect with `Last-Event-ID` (or `?cursor` for a brand-new `EventSource` — header wins when both are present) replays via `hub.Since` or sends `resync`; 15 s heartbeat comment pings keep the connection alive between events.
- `cmd/emsim/api.go`: `runAPI` now creates the `Hub`, starts `RunListener`, and blocks on its `ready` channel (bounded by a new 15 s `realtimeListenTimeout`) *before* building the public handler/HTTP routes or running C6's recovery — the ADR-018 ordering requirement. The scheduler and listener now share one `backgroundCtx`, cancelled together on shutdown.
- `internal/training/http/handlers.go`: `NewHandlers` gained a `*realtime.Hub` parameter; `trainingService` interface gained `Monitor`; new routes registered.

Validation:
- `go build ./...`, `go vet ./...`, `gofmt -l .`, `make verify`, `make verify-web` — clean.
- `go test ./...` — new `internal/platform/realtime` unit tests: replay-after-cursor, resync on unknown/malformed/superseded-epoch cursor, resync when the ring buffer has evicted the position, `Wait()` wakes on both `Publish` and `Reset`. Caught and fixed a real bug in the same pass: `NewHub`/`Reset` originally minted epochs from `time.Now().UnixNano()`, which collided when called back to back in a test (`NewHub()` immediately followed by `Reset()`) — switched to a `sync/atomic` monotonic counter.
- `go test -tags=integration -count=1 ./test/integration/...` — all green, including new `TestRealtimeListenerDeliversDomainNotifications` (a real `RunListener` against real PostgreSQL observes the `pg_notify` a live `Service.Execute` call issues, `hub.Since` resolves it correctly, and `hub.Reset()` forces the pre-event cursor to resync) and `TestTrainingMonitorSnapshotReflectsLiveState` (no rows before start since no run exists yet; two independent trainees' `active_items`/`queue_left`/`done`/`last_action` tracked separately; closing one item moves it into `Done` and offers the next queued version, visible on the very next `Monitor` read).

Open issues / risks:
- `serveSSE`'s heartbeat and event-write paths both call `flusher.Flush()` unconditionally on every wake, including a `hub.Wait()` wake that matched nothing for this subscriber (e.g. another lesson's event) — harmless (an empty flush), but worth trimming if it ever shows up as measurable overhead at the RFC's 30-participant scale.
- `presenceTracker` is exactly as RFC-001 §7.7 describes it: single-process, in-memory, lost on restart. Nothing in this slice persists it, and nothing should — a durable presence table is explicitly out of scope.
- Load/failure verification (reconnect mid-stream, resync after a killed connection, 30 concurrent SSE clients) is slice-4-plan.md's C11, not attempted here.

Next:
- C10 — `web: add group lesson builder live monitor and sse workplace`: instructor assignment builder (multi-row queue editor, `spawn_every_s` for hard), live monitor UI consuming `GET /lessons/{id}/monitor` + `GET /lessons/{id}/stream`, trainee workplace consuming `GET /my/stream` + `GET /my/run` + `GET /my/items` with stream-first snapshot ordering (RFC-001 §7.7), parallel-card/event/late/restart-interruption/stop display, and the post-close `control_report` UI. Minimal functional UI per `slice-planning.md` §1 — visual АРМ-112 styling is slice 12.

## 2026-09-20T18:15:00+03:00 — Claude — slice 4 C10 (group lesson builder, live monitor, SSE workplace)

Status: completed. This closes slice-planning.md §5's full C1–C10 (C11's cross-model e2e/failure-mode sweep is explicitly out of scope for this pass).

Changed:
- `web/src/api/realtime.ts` (new): `useEventStream(path, onInvalidate)` — a thin `EventSource` wrapper. `stream.ready`/`resync`/`invalidate` all just call `onInvalidate`; the browser's own native reconnect (every server event carries `id:`, i.e. `Last-Event-ID`) needs no help from this hook, since a reconnect's own `stream.ready`/`resync` triggers the same callback again.
- `web/src/api/workplace.ts`: `useMyRun`/`useMyItems`/`useItem` drop `refetchInterval: 2_000` (slice-4-plan.md's C10: "убрать двухсекундный polling как основной механизм") — SSE now drives invalidation; `refetchOnWindowFocus`/`refetchOnReconnect` stay as a fallback, not the mechanism.
- `web/src/api/training.ts`: `stopLesson`, `useMonitor`, `Level`/`Monitor`/`MonitorRow` types. `web/src/api/commands.ts`: `Command.type` gained `"control_report"`.
- `web/src/routes/instructor/Lessons.tsx`: level selector (easy/medium/hard) on create, with a conditional `spawn_every_s` field for hard (backend requires it fixed at creation, before any multi-item queue can be saved).
- `web/src/routes/instructor/LessonDetail.tsx`: rewritten from a single workstation/trainee/scenario form into a multi-row group builder — add/remove rows, unique workstation/trainee per row (disabled options, not just validation), an ordered per-row queue with add/reorder(↑↓)/remove, resolved to version ids via `useQueries` against each distinct scenario id in any row's queue at submit time (Assignment.scenario_version_ids needs version ids; the catalogue list only carries scenario ids + a version *number*). Added the Stop button (optional reason, ≤500 chars) and a link to the new monitor page.
- `web/src/routes/instructor/Monitor.tsx` (new): `GET .../monitor` snapshot, invalidated by `useEventStream(".../stream", ...)`. Table columns match `Monitor.rows[]` 1:1 (workstation, trainee, online, active_items with a restart-interruption marker, queue_left, done, last_action).
- `web/src/routes/trainee/Workplace.tsx`: `useEventStream("/my/stream", ...)` invalidates `my/run`/`my/items`/the open item on every stream signal. Added: an interruption banner (`item.interruptions`), a delivered-events list (`item.events`, with a late marker), and `ControlReportForm` — shown once an item is `closed`/`interrupted` instead of the old separate "Completed" screen, so the trainee can still submit a post-close message. This needed `lastItemId` (a "remember the last real item id across this mount" pattern, since `/my/run` disappears the instant the last item closes but the item itself must stay visible for control_report — explicitly scoped to the current mount only; a reload losing it is deferred, per slice-planning.md §5's own note).
- `internal/training/http/handlers.go`: `POST /lessons/{lessonId}/stop`'s missing `stop_reason` in the `Lesson` JSON response (present in the domain type since C1, never wired into `toLessonJSON` — a C8 gap, fixed here since the UI needed it to show why a lesson stopped). `design-docs/contracts/openapi.yaml`: `Lesson.stop_reason` added to match.
- **Bug found and fixed while wiring the UI**: `GET /my/run`'s `queue_left` counted currently-open (non-terminal) items — correct by coincidence in slice 3 (at most one item open at a time) but wrong once a hard-level run can have several parallel open items, where it double-counted them as "still queued" instead of counting only not-yet-offered items. `Monitor.rows[].queue_left` (this session's own C9 work) already used the correct semantics (`len(assignment.scenario_version_ids) - run.QueueCursor`); `Service.MyRun` now also returns the assignment's total queue length so the HTTP handler can compute `queue_left` the same way, keeping the trainee's own view consistent with the instructor's monitor for the same run. Signature change rippled through the `trainingService` interface, `fakeTraining`, and five integration-test call sites.

Validation:
- `go build ./...`, `go vet ./...`, `gofmt -l .`, `make verify`, `make verify-web`, `cd web && npm run lint` — clean.
- `go test -tags=integration -count=1 ./test/integration/...` — all green, including the `queue_left` semantics fix (`training_e2e_test.go`'s `currentItemID` helper asserted the old, now-wrong value; updated with the reasoning documented inline).
- Full manual browser walkthrough against `docker compose up --build` (not just `go test`): created a second trainee and reset the seed instructor's password via the admin UI; created a hard-level lesson with `spawn_every_s`; built two assignment rows through the new multi-row editor, including a `spawn_card` scenario whose queue-plan validation genuinely rejected an incomplete queue (`"spawn_card has no next queue scenario"`) — confirming the UI surfaces a real backend rule, not just client-side checks; saved, started, opened the monitor (`active_items`/`queue_left`/`done`/`online` all correct); logged in as the trainee, opened/accepted a card, watched the `notice` and `spawn_card` delivered events and the newly spawned parallel card appear; restarted the `api` container mid-session and confirmed C6's restart-interruption banner rendered on both open items after reload; closed a card and submitted a `control_report`, confirmed in `control_reports` via `psql` and reflected in the monitor's `last_action`; the hard scheduler's own `spawn_every_s` interval independently offered a second parallel card during testing, confirmed in the monitor; stopped the lesson with a reason, confirmed `lessons.stop_reason` displayed; confirmed the `lesson.close` task reached `done` and every remaining item closed, and the lesson itself reached `finished`, without any further UI action. Two real bugs (see above and the `queue_left` note) were caught only by this manual pass, not by any automated test — both fixed and covered before this commit.

Open issues / risks:
- The multi-row assignment editor cannot show a draft lesson's already-saved queue contents (`Assignment.scenario_version_ids` are version ids; reconstructing "which scenario, in what order" needs a lookup this UI does not attempt) — editing re-enters queue-empty and must be re-built wholesale rather than adjusted in place. Documented inline; acceptable under slice-planning.md §1's minimal-UI bar, revisit only if instructors find re-entering a queue on every edit too costly before slice 12.
- Presence (`online`) was exercised mechanically (unit-level ref-counting) but not observed live across two simultaneous browser sessions in this pass — the manual walkthrough's two roles shared one cookie-jar browser context, so only one session was ever authenticated at a time. The underlying mechanism (`presenceTracker`, SSE connect/disconnect) is otherwise covered by the C9 unit/integration tests.
- `C11` (cross-model end-to-end test sweep: concurrent multi-participant scenarios, reconnect-mid-stream, 30-client load, restart under real concurrent traffic) is unstarted — this session's manual pass and the C6–C10 automated tests are not a substitute for it.

Next:
- C11 — `test: verify slice 4 end to end and document acceptance`: the full cross-cutting test sweep slice-4-plan.md itself describes (concurrent multi-participant runs, hard-mode parallel-issuance races, restart-under-load, SSE reconnect/resync under a killed connection, `control_report` replay/stale-seq), plus updating README/LOG.MD's final acceptance notes. Not attempted in this session — `slice-4-plan.md`'s own scope boundary for this delivery stopped at C10.

## 2026-09-20T21:17:35+03:00 — Claude Sonnet 5 — slice 4 C11 (end-to-end verification and acceptance)

Status: completed. This closes slice-planning.md §5 / slice-4-plan.md's full C1–C11.

Changed (tests only, plus README's own acceptance notes — no production code changed):
- `test/integration/training_service_test.go` (+4 tests):
  - `TestTrainingHardQueueOffersOneCardPerTickAfterLongOutage` — RFC-001 §7.2's "за tick выдаётся не более одной карточки на run, пропущенные интервалы не воспроизводятся пачкой": pushes a 3-card hard run's `next_offer_at` an hour into the past (many multiples of a 5 s `spawn_every_s`), asserts one `Tick` offers exactly one new card (not a catch-up batch) and computes the next `next_offer_at` from the real offer time (still in the future), and that an immediate second `Tick` adds nothing more.
  - `TestTrainingRecoverDeliversOverdueEventAsLate` — RFC-001 §7.2's restart recovery: an item_event's `due_at` is pushed an hour into the past to simulate an outage; `Recover` (C6) marks the item's interruption without touching that `due_at` (asserted directly against the pre-outage value, "дедлайны не сдвигаются"); the next `Tick` delivers it with `late=true`, visible both in the raw row and in `Service.ItemForTrainee`'s own `DeliveredEvent` projection.
  - `TestTrainingControlReportRejectsStaleExpectedSeq` — `control_report` (C7) goes through the same `expected_seq` optimistic-lock path as every other command: a second attempt against an already-stale seq is rejected `stale_seq`, not silently accepted or conflated with a replay, and does not insert a second `control_reports` row; the same command at the now-current seq applies normally as a second, independent report.
  - Two existing test-fixture login strings in the new hard-outage test originally exceeded `users_login_shape`'s `{3,64}` regex bound once `+uuid.NewString()` (36 chars) was appended — caught immediately by a real `storage failure` on the very first run against a real database, not a review; shortened the prefixes.
- `test/integration/training_realtime_e2e_test.go` (new file) — the one piece of slice-4-plan.md's C9/C11 nothing else exercised: `GET /lessons/{id}/stream` and `GET /my/stream` driven over real HTTP against a live `emsim api` process (RFC-001 §7.7 end to end, not just `Hub.Since`/`Reset` in isolation or `LISTEN`/`NOTIFY`→`Hub` as `realtime_test.go`'s C9 test already covered). One test, `TestAPIProcessSSEStreamSnapshotReplayResyncAndFiltering`: stream-first snapshot ordering (`stream.ready` always precedes anything else on both a fresh connect and a reconnect); a domain command's `pg_notify` reaches the instructor's lesson stream and the acting trainee's own `/my/stream` as `invalidate`, but never a second trainee's stream (no lesson at all — filtering is by user id there, not by lesson); reconnecting with `Last-Event-ID` replays exactly what was missed while disconnected; the `?cursor` query fallback (what a brand-new `EventSource`, which cannot set headers, must use for a page-reload reconnect) replays the same way; an unresolvable cursor (a made-up epoch) always gets `resync`, per ADR-018, never a silently-dropped gap. Added a small local `sseClient` (a goroutine draining `bufio.Scanner` lines into a channel so `next(timeout)` can time out instead of blocking forever on a connection that sends nothing) and `sseHTTPClient` (shares the authenticated session's cookie jar but drops the 5 s `Timeout` the rest of this package's short-lived JSON-request clients use, which would otherwise kill a still-open streaming body mid-test).
- `README.md` — one paragraph under "Разработка" naming where slice 4's own coverage lives at each of its three levels (unit `Hub`, `training.Service` against real PostgreSQL, HTTP against a live `api` process), so a future reader does not have to rediscover this split from LOG.MD alone.

Validation:
- `go build ./...`, `go vet ./...`, `gofmt -l .` — clean.
- `go test ./...` (unit, no `integration` tag) — clean, unaffected.
- `go test -tags=integration -count=1 ./test/integration/...` — full suite green (`ok`, 92.2 s), including all 4 new tests individually verified first (`-run` scoped, `-v`) against a fresh Postgres/Ryuk pair each, then again as part of the whole package to rule out cross-test interference (shared login prefixes, leftover containers).
- `make verify`, `make verify-web` — clean; `web/dist/.gitkeep` restored after `vite build` deleted it, per the established routine.
- Did not attempt: a real 30-concurrent-client SSE load test (RFC-001 §11's own capacity numbers) or an actual process-kill-mid-command restart drill (C6's own test already simulates the DB-side effect of a restart directly, which is what Recover's own contract governs — a literal `SIGKILL` of a live `emsim api` process during an open command is `slice-planning.md`'s §12 (visual polish + приёмка/load profile), not this slice's own DoD).

Open issues / risks: none new. `slice-4-plan.md`'s C1–C11 are all complete; slice 4 itself (slice-planning.md §5) is done. The three items `slice-4-plan.md`'s C9 log entry flagged as deferred to C11 (SSE reconnect/resync under a killed connection, per-user filtering, cursor replay) are now covered by `training_realtime_e2e_test.go` above; the fourth (30-concurrent-client load) remains explicitly out of scope until slice 12's load profile.

Next: slice 5 — телефон и запись доклада (slice-planning.md §6).

## 2026-09-20 — Slice 5 C1–C2

Status: C1 completed; C2 storage foundation completed.

- C1 records phone/recording command, authorization, deadline and immutable voice-manifest contracts in ADR-005, OpenAPI and contract checks.
- C2 adds migration 00008 (`blobs`, `voice_assets`, `calls` with one-active-call partial unique index), schema readiness v8, and typed training persistence ports for content-addressed blobs, voice assets and calls.
- Validation: `python3 design-docs/contracts/check.py`; focused `go test ./internal/training/... ./internal/platform/postgres/...`.

Next: C3 filesystem blob adapter and administrative voice-asset import.

## 2026-09-21 — Slice 5 C3–C7

Status: implemented.

- Added content-addressed filesystem blobs and CLI import of immutable contact greeting/ack assets; Compose shares `blob-data` with seed, API and worker.
- Added phone call rules, persistence, close/stop upload deadlines, call evidence and protected media upload/download paths.
- Added the minimal browser phone panel: contact selection, microphone fallback, MediaRecorder, call report fields, in-memory recording upload/retry status and recording state display.
- Validation run during work: focused Go training/API tests and `cd web && npm run check && npm run lint`.

Next: run the complete repository verification sweep and address any integration regressions.

## 2026-09-20T21:50:43+03:00 — Claude Sonnet 5 — slice 4 review fixes (scheduler starvation, SSE staleness, late/spawn semantics)

Status: completed. Fixes four findings from a post-C11 review of slice 4 (not itself a new C-step) — one critical, three significant. No plan/scope change.

Changed:
- `internal/training/service.go`'s `Tick`: no longer aborts its whole batch on the first `tickHardRun`/`tickEvent` error — each due run/event is now independent, errors are `errors.Join`ed and returned (still logged by `runTrainingScheduler`) but never stop the rest of the batch or a later Tick. **Root cause fixed, confirmed by reproduction before the fix**: a hard-level run's own `next_offer_at` tick can legitimately consume the same queue slot a `spawn_card` event was waiting to issue (`checkSpawnQueuePlan` only proves the *static* plan is reachable, not that runtime issuance order matches it); the resulting mismatch was returned as a plain error on *every* Tick forever (the event stayed `scheduled`, and `ScheduledItemEventsDue`'s `due_at` ordering kept re-selecting it first), which — combined with Tick's old abort-on-first-error — starved every other running lesson's due events/offers, not just this one. `tickEvent` now converts that specific, deterministic mismatch (`isSpawnPlanMismatch`, matching `spawnEventCard`'s own `scenario_version_ids` `ValidationError`s) into a terminal `skipped` event (new `SkipReasonSpawnPlanMismatch`) instead of an eternal error; other error classes (content lookup failures) still propagate as real errors.
- `tickEvent`'s `late` flag: no longer `delivered_at - due_at > 5s` alone — that also fired for an event that legitimately waited on `status_in` for a while with no server outage at all, contradicting `schema.sql`'s own comment on `item_events.late` ("доставлено позже due_at более чем на 5с (простой сервера)"). Now additionally requires the item to carry an interruption (`RecoverOpenItems`, C6) detected at or after the event's own `due_at` (new `interruptedSince` helper) — i.e. the scheduler was actually down for some of the time this event was pending. `TestTrainingRecoverDeliversOverdueEventAsLate` (existing) still passes unchanged since it explicitly calls `Recover` before `Tick`.
- `offerQueueVersion` gained an optional `*spawnOrigin{itemID, variation}` parameter (nil at every ordinary call site — start, a normal close, a hard-scheduler tick); `spawnEventCard` is now the only caller that passes one. This makes two previously-dead pieces of ADR-018 real: `items.spawned_from` (column existed, was never written) is now set to the spawning item's id, and a `duplicate` spawn_card's own `variation` text (schema.json: "чем отличается") is folded into the new item's own card `incident.description` via new `appendVariation` — the scenario body itself is immutable and shared with the original item, so the distinguishing detail can only live on the spawned item's own mutable card copy. `internal/training/dds/evidence.go` now also projects `item.SpawnedFrom` into `EvidenceBody.SpawnedFromItemID` (evidence.schema.json's `spawned_from_item_id`, previously always omitted).
- `internal/training/http/realtime.go`'s `serveSSE`: the heartbeat tick now also re-checks `Since(cursor)` (falling back to the plain `: heartbeat` ping only when there is nothing to replay) instead of being a pure keep-alive. Root cause: `h.hub.Wait()`'s channel is re-armed fresh on every loop iteration, so a `Publish` landing in the (ordinarily tiny) gap between an earlier wake's `replaySince()` returning and the loop reaching `select` again closes a waiters channel nobody is blocked on yet. That is *not* a lost event — `Since(cursor)` always catches up from `cursor` whenever next called — but with nothing re-checking it proactively, the connection would otherwise only catch up on the *next* `Publish` anywhere in the whole process (`Wait()` is hub-global, not per-lesson/user), which self-heals almost instantly in a busy classroom but has no bound at all in an otherwise-quiet one (e.g. the very last event of a session). Re-checking on every heartbeat caps that staleness at `heartbeatInterval` (15s) even in the fully idle case.
- `test/integration/training_service_test.go` (+2 tests): `TestTrainingTickSurvivesSpawnPlanMismatchAndDeliversOtherEvents` reproduces the exact hard-offer/spawn_card race (confirmed it failed before the fix — an unrelated second lesson's own due `notice` event was never delivered) and asserts both that Tick keeps flowing and that the mismatched event reaches a terminal `skipped` state with the right reason, not left `scheduled` forever. `TestTrainingSpawnCardSetsSpawnedFromAndAppendsVariation` asserts the spawned item's `SpawnedFrom`, its card's variation text, and evidence's `spawned_from_item_id` after close — using `kind=scenario` rather than `kind=duplicate` to reach a queue `checkSpawnQueuePlan` actually accepts (see below).

Validation:
- `go build ./...`, `go vet ./...`, `gofmt -l .` (repo, minus `orchestration-core-main`) — clean.
- `go test ./...` (unit) — clean.
- `go test -tags=integration -count=1 ./test/integration/...` — full suite green (87s), including both new tests and every pre-existing slice 3/4 training/realtime/e2e test (no regressions from the `Tick`/`late`/SSE changes).
- `make verify` — clean (staticcheck included).

Open issues / risks found but not fixed here (out of this pass's scope):
- `checkSpawnQueuePlan` (assignment-time static validation) appears to make **any** queue containing a `kind=duplicate` spawn_card event unsatisfiable: its own rule requires the next queue slot to be the *same* scenario version, but that next occurrence carries the identical spawn_card definition and is validated the same way, demanding yet another occurrence after it — a finite queue can never close the chain. Discovered while writing this session's variation test (worked around with `kind=scenario` instead); no seed content currently uses `kind=duplicate`, so this has not surfaced yet, but it should be looked at before slice 11 (scenario authoring) makes `duplicate` reachable from the instructor UI.
- The SSE heartbeat self-heal fix has no dedicated fast unit test — `heartbeatInterval` is an unexported 15s `const`, and a real test would either need to wait a genuine 15s or have that constant made injectable, which felt like scope creep for this pass. Covered indirectly: the full existing SSE integration suite (`training_realtime_e2e_test.go`) still passes unchanged.
- The other slice-4-review items (heavier scheduler locking than RFC's own SKIP LOCKED design, Stop not invalidating trainee SSE streams directly, `Recover` running without a lessons barrier, thin conurrency test coverage for "command vs stop"/"close vs next-card", `sse-events.schema.json` not reflecting the actual `invalidate`/`stream.ready`/`resync` wire shape) were flagged as "existing/deviation" rather than critical/significant in the review and are still open.

Next: slice 5 — телефон и запись доклада (slice-planning.md §6), or a follow-up pass on the `checkSpawnQueuePlan` duplicate-chain gap above if the user wants it closed first.

## 2026-09-20T22:20:23+03:00 — Claude Sonnet 5 — slice 4 review fixes round 2 (scheduler lock strength, hard-run-after-stop, trainee SSE on stop, Recover barrier)

Status: completed. Fixes the five remaining "отклонения от плана/RFC" the same review flagged (not a new C-step). No plan/scope change.

Changed:
- `internal/training/service.go`'s `tickEvent`: `lessons` lock downgraded from `FOR UPDATE` to `FOR SHARE` (it never writes to `lessons`, only `Stop`/`Start`/`FinishLesson` do — RFC-001 §7.2's own scheduler order is "lessons FOR SHARE → items FOR UPDATE → item_events..."); `runs FOR UPDATE` is now taken only when the event is actually `spawn_card` (the one case that writes `runs.queue_cursor`/`next_offer_at`), resolved from an unlocked peek read of the item's own immutable `scenario_version_id` before any lock is acquired, same "read what never changes without a lock" pattern `lockForCommand` already uses. Before this fix, every due event — including one merely re-checked while waiting on `status_in`, every 500ms — took an exclusive lock on the whole lesson for its transaction's duration, blocking every trainee's own `FOR SHARE` command on that lesson.
- `tickHardRun`: same `lessons FOR SHARE` downgrade (never writes to `lessons` either), plus a defensive `SetRunNextOfferAt(nil)` when the run is found not tickable any more (belt-and-suspenders alongside the `Stop` fix below).
- `Service.Stop` now also calls new `ClearNextOfferForActiveRuns` (bulk `UPDATE runs SET next_offer_at=NULL WHERE lesson_id=$1 AND state='active'`) in the same barrier transaction. **Root cause fixed**: `RunsDueForOffer` only ever filtered on the run's own state, never its lesson's, so a stopped hard-level run's `next_offer_at` stayed selectable — `tickHardRun` correctly no-op'd every time (nothing was corrupted), but re-locked the lesson and rediscovered "not running" on every single 500ms tick, forever, until the durable `lesson.close` task eventually finished the run — which may be delayed, or (worker unavailable) never happen at all.
- `Service.Stop` also now calls `notify` once per active run's own trainee (`RunsByLesson`), not only the lesson-scoped `notify(..., uuid.Nil, uuid.Nil)`. **Root cause**: `streamMy` (a trainee's own SSE feed) filters by `UserID`, which a `uuid.Nil`-userID event never matches — so a trainee only ever learned their lesson had stopped once the durable `lesson.close` task later interrupted an open item of theirs (or, with none open, only on the next unrelated refetch). Confirmed by temporarily reverting this one change and re-running the new regression test below — it failed exactly as expected, then passed again once restored.
- `Service.Recover` no longer runs one cross-lesson bulk `UPDATE` under no explicit lock at all. It now enumerates running lessons (new `RunningLessonIDs`, unlocked), then recovers each one in its own transaction under that lesson's own `lessons FOR UPDATE` — the same lock `Start`/`Stop` already use — matching RFC-001 §7.2's "в транзакции под барьером занятия" literally instead of relying entirely on "nothing can race this because HTTP isn't listening yet during this MVP's single recovery call." `RecoverOpenItems`'s own signature changed to take the already-locked `lessonID` and dropped its own `lessons` join/state filter (the caller has already verified `state='running'` under lock).
- `internal/training/store.go`/`postgres/store.go`: `Store` interface and implementation updated for `RunningLessonIDs` and `RecoverOpenItems`'s new signature; `ClearNextOfferForActiveRuns` added.
- `test/integration/training_service_test.go` (+5 tests): `TestTrainingStopClearsHardRunNextOfferAt` (next_offer_at is NULL right after Stop, a further Tick offers nothing); `TestTrainingRecoverAcrossMultipleRunningLessons` (two running lessons, one `Recover` call marks both, idempotent per-lesson, a new recovery_id adds a second marker to both — the per-lesson loop must not regress the old bulk statement's completeness); `TestTrainingConcurrentCommandVsStopRespectsBarrier` (a real goroutine race between a trainee's open and the instructor's Stop, asserting the barrier invariant against `stop_cutoff_log_seq` rather than assuming an interleaving — passed `-race -count=5` with no flakiness); `TestTrainingConcurrentCloseOffersNextCardExactlyOnce` (two racing close attempts on the same item, asserting the queue advances by exactly one card, not zero or two — also `-race -count=5` clean); `TestTrainingThreeParticipantsIndependentQueues` (slice-planning.md §4's DoD line literally says "минимум трём участникам," the existing group test only ever covered two).
- `test/integration/realtime_test.go` (+1 test): `TestRealtimeListenerDeliversStopNotificationToTrainee`, driving a real `RunListener`+`Hub` against real PostgreSQL to observe Stop's own per-trainee `pg_notify`. Confirmed to fail without the fix (temporarily reverted the per-trainee notify loop, reran, got the expected failure, restored).
- `test/integration/lesson_close_worker_e2e_test.go` (new file): `TestWorkerProcessDrivesLessonCloseThroughRealQueue` — builds the real `emsim` binary, starts a real `emsim worker` process (reusing `worker_process_test.go`'s own harness), and drives `Stop` → the durable `training.KindLessonClose` task → a finished lesson entirely through that real process's own `lessonCloseHandler`, not a direct in-test call to `Service.CloseStoppedLesson` (every existing lesson.close assertion only ever exercised the latter). Also checks a repeat `Stop` on the now-finished lesson (with the worker since stopped) stays idempotent — no duplicate task row.

Validation:
- `go build ./...`, `go vet ./...`, `gofmt -l .` (repo, minus `orchestration-core-main`) — clean.
- `go test ./...` (unit) — clean.
- `go test -tags=integration -count=1 ./test/integration/...` — full suite green (~100s), including all 6 new tests and every pre-existing test (no regressions from the lock-strength, Stop, or Recover changes).
- `go test -tags=integration -race -count=5 -run 'TestTrainingConcurrentCloseOffersNextCardExactlyOnce|TestTrainingConcurrentCommandVsStopRespectsBarrier'` — clean, no data races, no flakiness across 5 repeats each.
- `make verify` — clean.

Open issues / risks: none new beyond what the first review-fixes entry above already listed (`checkSpawnQueuePlan`'s duplicate-chain gap; the SSE heartbeat self-heal fix's own lack of a fast unit test). The remaining review item this pass did not touch — `sse-events.schema.json` not reflecting the actual `invalidate`/`stream.ready`/`resync` wire shape — is a contract-documentation gap, not a behavior bug; still open.

Next: slice 5 — телефон и запись доклада (slice-planning.md §6).

## 2026-09-21 — Slice 5 C8–C9

Status: completed.

- Added immutable `pilot-phone-01` and the selected final greeting/ack as
  PCM WAV (mono, 16-bit, 16 kHz); offline seed imports their immutable
  manifests into the shared content-addressed blob volume idempotently.
- Enabled required-call scenarios in lesson assignment and fixed the runtime
  image ownership order so `/app/blobs` is created before switching to UID
  65532.
- Added end-to-end acceptance coverage through the real seed CLI and HTTP API:
  independent active calls, command replay, phrase download, manifest upload,
  upload replay/conflict, `recording=null`, close/evidence, protected instructor
  download, lost in-memory bytes and deadline expiry.
- Recording upload conflicts now expose the contracted
  `recording_conflict` and `recording_deadline_passed` API codes instead of a
  generic `conflict`.
- Browser-only acceptance steps for Chrome, Edge and Яндекс Браузер are
  documented in README; they require an interactive host with those browsers
  and microphone controls.

Validation: `make verify`, full PostgreSQL/Testcontainers integration suite
(103s), web lint/typecheck/build, Compose config and `docker compose build` —
all green. Chrome is available on this host; Edge and Яндекс Браузер are not
installed, so the documented three-browser manual matrix could not be run here.

Next: slice 6.

## 2026-09-21 — Slice 6 C6–C9

Status: completed.

- C6 completed the worker-side assessment pipeline: coordinator seals immutable inputs and promotes waiting evaluate tasks; the handler/finalizer creates exactly one `auto` revision, and expert revisions cancel outstanding evaluation atomically. The package includes deterministic DDS rules, PostgreSQL storage and recovery composition.
- C7 adds instructor-only assessment API endpoints for one item, manual revisions, and the closed-card review queue. Foreign and unclosed resources remain indistinguishable as 404; stale revisions are 409 and request validation is 422.
- C8 adds the instructor review queue and item-review UI with evidence, criterion explanations, call playback, revision history and an expert override form. Closed/stopped lesson pages and the monitor link to the queue.
- C9 adds `TestAssessmentPipelineThroughAPIAndWorker`: a live API plus separate worker process produce auto rev=1 after a real trainee close; the instructor reads the queue and creates rev=2 through the public API.

Validation: focused Go unit/handler tests, `go test -tags integration -run '^TestAssessmentPipelineThroughAPIAndWorker$' ./test/integration/...`, and `cd web && npm run check` passed during implementation. Full repository gates are the next verification step.

## 2026-09-22 — План вертикальных срезов оператора 112

Status: planned; реализация не начата.

- Добавлен `slice-planning-112.md`: девять вертикальных срезов от первого текстового вызова и карточки до профильных опросных карт, взаимодействия с ДДС, отдельной оценки, редактора, сложных групповых занятий, ИИ-заявителя и голосового режима. Каждый срез включает пользовательский результат, объём и критерии приёмки.
- По текущему решению пользователя этап 112 начинается до завершения оставшихся работ ДДС. Новый план явно заменяет прежнюю очередность из `slice-planning.md`/ADR-015; разделение типов упражнений и общие гарантии платформы сохраняются.
- Эмуляция телефона планируется в браузере: сначала текст, затем голос; настоящий SIP и совместное упражнение двух обучаемых не являются зависимостями. Интерактивная модель требует отдельного пересмотра ADR-003 и измерения ресурсов перед срезом 112-8.
- Код, контракты и существующие данные не изменены. Проверка документа: структура срезов, локальные ссылки и отсутствие ошибок whitespace; тесты приложения для изменения только документации не запускались.

Next: детализировать срез 112-1 по контрактам, миграциям, API и одному пилотному сценарию.

## 2026-09-23 — Карточка оператора 112 по рабочему образцу

Status: implemented in working tree; commit not created.

- Рабочее место 112 перестроено по предоставленным скриншоту и инструкции: телефонная панель, данные заявителя, подробный адрес, описание, статус пострадавших и нижняя панель действий. Поля карточки сохраняются и входят в неизменяемый снимок отправки; список служб пока ограничен «03».
- Добавлены завершения «нет контакта» и «срыв звонка» без отправки, контрактные поля, миграция и отображение новых данных в преподавательском разборе. Выбор «что случилось» и дополнительная карточка происшествия оставлены для следующего этапа; пока их отсутствие не должно ухудшать ручную оценку полноты.
- Изменены `internal/training/`, `migrations/`, `design-docs/contracts/`, `web/src/`, `web/e2e/`, интеграционные тесты и документация.

Validation: `make verify`, `make verify-web`, `npm run lint`, `python3 design-docs/contracts/check.py`, полный набор `go test -tags=integration -count=1 ./test/integration/...` и три браузерных E2E сценария (112 и регрессия ДДС) прошли; `git diff --check` без замечаний.

Next: реализовать выбор типа происшествия и связанную профильную карточку, затем уточнить состав служб по следующему срезу.

## 2026-09-23 — Срез 112-2: подготовленный опрос заявителя

Status: implemented.

- Добавлены версионированный диалог сценария, проверка фактов и зависимостей вопросов, отдельный порт симулятора заявителя и подготовленная реализация. Старые версии 112-1 продолжают использовать прежний текстовый вызов.
- Правила и API сохраняют заданные вопросы, ответы, время и состояние удержания; обучаемому доступны только уже произнесённые реплики и текущие вопросы. Преподаватель видит факты, диалог, сохранения карточки и снимок отправки. Новая миграция расширяет типы действий.
- Поставлены версия 2 медицинского пилота и два задания для уточнения адреса, числа пострадавших и неизвестных заявителю сведений. Обновлены `internal/content/`, `internal/training/`, `internal/platform/postgres/`, `migrations/`, `design-docs/contracts/`, `seed/`, `web/`, README и сквозные проверки.

Validation: `make verify`, `make verify-web`, `cd web && npm run lint`, `python3 design-docs/contracts/check.py`, полный `make test-integration` на PostgreSQL 16 через Docker и `cd web && npm run test:e2e` (112 и регрессия ДДС) прошли; `git diff --check` без замечаний. Ранний интеграционный прогон выявил устаревшее ожидание числа сценариев; повтор после исправления прошёл. Ещё один прогон пересёкся со сборкой клиента и прервался на временно отсутствующем `web/dist`; изолированный повтор прошёл.

Remaining risks: ручная приёмка сценарных формулировок и экранов профильным экспертом ещё требуется. Свободный текст, профильные карты и автоматическая оценка остаются в будущих срезах.

Next: срез 112-3 — профильные карты, классификация и подбор служб.

## 2026-09-23 — Уплотнение основной карточки 112

Status: implemented in working tree; commit not created.

- Верхняя панель 112 разделяет учебную доступность телефонии и состояние разговора; ручной выбор сохраняется для пользователя и РМ в браузере, открытая карточка и 10 секунд после закрытия показывают «Недоступен». Телефоны, сведения о происшествии и действия вызова уложены в компактную панель.
- В строке заявителя оставлены ФИО, статус и оператор/канал связи. Возраст и признак иностранного языка убраны из формы, но сохранены в формате старых карточек. Новая карточка не подставляет оператора; прежнее значение `phone` остаётся читаемым и допустимым. Адрес отображается целиком без внутренней прокрутки, нижняя панель больше его не перекрывает.
- Изменены `internal/training/intake.go`, `design-docs/contracts/openapi.yaml`, `web/src/`, браузерный сценарий 112 и README. Дополнительная карточка происшествия не менялась.

Validation: `make verify` (с отдельным временным Go cache), `make verify-web`, `cd web && npm run lint`, `python3 design-docs/contracts/check.py`, полный `make test-integration` на PostgreSQL 16 через Docker и `cd web && npm run test:e2e` (3 сценария) прошли. Снимок из браузерного теста осмотрен: адрес и нижняя панель не пересекаются при 1366×768; `git diff --check` без замечаний.

Remaining risk: статус телефонии учебный и локален для браузера; он не отражает SIP-соединение и не синхронизируется между устройствами. Ручная приёмка внешнего вида профильным экспертом ещё требуется.

Next: согласовать экран с экспертом и продолжить следующий срез 112.

## 2026-09-23 — Детализация плана 112-3 по картам 104/101

Status: planned; код и контракты реализации не изменялись.

- Добавлен `slice-112-3-plan.md`; раздел 112-3 в `slice-planning-112.md`
  согласован с тремя запрошенными кейсами: взрыв газа, ДТП с пламенем и их
  комбинация. Образцы карт 104/101 сохранены и описаны в `docs/reference-ui/`.
- Зафиксировано разделение назначения кейса и выбора типа происшествия:
  профильные карты появляются только после явного выбора обучаемого. Для новых
  кейсов предусмотрен режим без симуляции разговора, а первые версии карт и
  пилотные правила предложения служб описаны отдельно от закрытого эталона.
- Несоответствие образца 104 названию «взрыв газа» отмечено для профильной
  приёмки: изображение показывает вопросы о запахе газа и не определяет
  признаки взрыва. Также остаются уточнения о множественном выборе и точном
  составе служб.

Validation: `git diff --check`; тесты приложения не запускались, поскольку
изменены только план, журнал и изображения-референсы.

Next: после согласования пилотных полей и правил реализовать вертикальный
срез по `slice-112-3-plan.md`.

## 2026-09-23 — Срез 112-3: типы происшествий и карты 104/101

Status: техническая реализация завершена; профильная приёмка пилота ожидается.

- Добавлены отдельные версии кейсов «Взрыв газа», «ДТП с пламенем» и их
  комбинации без симуляции разговора. Назначенный кейс не содержит карт:
  обучаемый явно выбирает тип, после чего появляются пустые карты 104/101.
- В `internal/content/`, `internal/training/`, `migrations/00014_operator112_profiles.sql`
  и `seed/` добавлены неизменяемый каталог типов, карт и правил предложения
  служб, фиксация его версии для занятия, команды добавления/удаления типа,
  сохранения карточки, выбора служб и завершения. Неактивные ответы можно
  восстановить; в итоговый evidence они не входят. Разговорные кейсы 112 и
  обработка ДДС сохраняют прежние маршруты.
- Обновлены `design-docs/contracts/`, API, веб-клиент, экран обучаемого,
  монитор и разбор преподавателя, README и `slice-112-3-plan.md`. Добавлены
  проверки правил, импорта, версионности, replay/stale/stop, evidence и
  браузерный сценарий всех трёх назначений.

Validation: `gofmt`, `GOCACHE=/private/tmp/emsim-go-cache make verify`,
`python3 design-docs/contracts/check.py`, `make verify-web`,
`cd web && npm run lint` (один не блокирующий warning), полный
`GOCACHE=/private/tmp/emsim-go-cache make test-integration` на PostgreSQL 16
через Docker, `cd web && npm run test:e2e` (4 сценария) и `git diff --check`.
Снимок комбинированной карточки осмотрен: панели карт и выбора служб видны
целиком. Первые полные интеграционные прогоны выявили устаревшие ожидания
числа seed-объектов и перечня таблиц; после исправлений полный прогон прошёл.

Remaining risk: профильный эксперт должен подтвердить достаточность образца
104 для «взрыва газа», кратность/обязательность ответов и правила предложения
служб, включая роль 102. До этого пилотные правила не считаются нормативными.

Next: провести профильную приёмку по вопросам `slice-112-3-plan.md`, затем
при необходимости выпустить новую версию каталога без изменения выданных
заданий.

## 2026-09-23 — Ограничение каталога в ответе обучаемому 112-3

Status: исправлено после проверки среза.

- `internal/training/http/handlers.go` теперь выдаёт обучаемому только названия
  типов и определения активных карт. Внутренние правила подбора служб, связи
  ещё не выбранных типов с картами и ответы удалённых карт в его ответ не
  входят. Доступные учебные службы для ручного выбора передаются отдельным
  списком без условий правил; преподавательский разбор сохраняет полный
  каталог. Обновлены OpenAPI, клиент, README и план.
- Добавлены тест проекции API и утверждения браузерного сценария до и после
  добавления типа. При регрессионном прогоне обнаружено перекрытие адреса
  липкой нижней панелью разговорного экрана; панель возвращена в поток страницы.

Validation: `make verify`, `make verify-web`, `npm run lint` (один прежний
warning), `python3 design-docs/contracts/check.py`, четыре браузерных E2E
сценария и `git diff --check` прошли. Полный интеграционный набор из предыдущей
записи не повторялся: хранение и БД не менялись.

Remaining risk: профильное подтверждение полей и правил служб остаётся
открытым. Next: получить решения по вопросам в `slice-112-3-plan.md`.

## 2026-09-23 — Проверка контракта evidence для 112-3

Status: техническая проверка завершена.

- `design-docs/contracts/evidence.operator112.schema.json` уточняет режим
  `card_only`: в evidence нет разговора и отправки, а завершённый кейс содержит
  выбранный тип и зафиксированный выбор служб.
- `test/integration/operator112_profiles_test.go` валидирует реальные evidence
  всех трёх кейсов и остановленного задания по контрактной схеме, а также
  проверяет, что схема отклоняет некорректное состояние вызова и завершение
  без выбора служб. Остановленное задание теперь закрывает реальный процесс
  `emsim worker` через долговечную задачу `lesson.close`, а не прямой вызов
  сервиса в тесте.

Validation: `python3 design-docs/contracts/check.py`,
`go test -tags integration -run '^TestOperator112ProfileCases$' -count=1
./test/integration/` на PostgreSQL 16 и `git diff --check` прошли.
Оставшийся шаг: профильная приёмка вопросов из `slice-112-3-plan.md`.

## 2026-09-23 — Очередь кейсов 112 и возврат к списку

Status: исправлено.

- Убрано ограничение одного сценария 112 в назначении (`internal/training/service.go`,
  `web/src/routes/instructor/LessonDetail.tsx`). Несколько кейсов теперь
  сохраняются в выбранном порядке; следующий выдаётся после завершения
  текущего существующим механизмом очереди.
- `web/src/routes/trainee/Workplace.tsx` сохраняет список кейсов в открытом
  рабочем месте после завершения последнего прогона и позволяет вернуться к
  нему из закрытой карточки. Обновлены подписи списка, README и web/README.
- Расширены интеграционная и браузерная проверки последовательного назначения
  и возврата к списку (`test/integration/operator112_profiles_test.go`,
  `web/e2e/operator112-profiles.spec.ts`).

Validation: `go test ./internal/training/...`, адресный интеграционный тест
`TestOperator112ProfileCases` на PostgreSQL 16, `make verify-web`,
`cd web && npm run check`, `cd web && npm run lint` (один прежний warning),
`cd web && npm run test:e2e` (4 сценария) прошли.

Remaining risk: список завершённого прогона сохраняется до ухода со страницы;
после перезагрузки просмотр обработанных кейсов доступен через историю.

## 2026-09-23 — Дополнение очереди через форму назначения

Status: исправлено после пользовательской проверки.

- На форме преподавателя после первого добавления селектор больше не
  возвращается к пустому значению: выбирается следующий доступный кейс,
  поэтому кнопка «+ В очередь» остаётся доступной.
- Повторное сохранение назначения добавляет новые кейсы после уже сохранённых
  версий, не заменяя их. Сохранённую часть можно очистить явно до запуска.
- Браузерный тест проверяет два добавления подряд, затем добавление третьего
  кейса после сохранения и неизменность количества версий в API.

Validation: `cd web && npm run check`, `cd web && npm run lint` (один прежний
warning), `cd web && npm run test:e2e` (4 сценария), `git diff --check` прошли.
Основной локальный стек пересобран через `docker compose up --build -d --wait`;
API и worker достигли `healthy`.

## 2026-09-24 — Вёрстка карточки 112-3 по инструкции оператора 112

Status: реализовано, проверено.

- `web/src/routes/trainee/Operator112ProfileCase.tsx` переписан по образцу
  рис. 37 инструкции (`docs/reference-ui/112-instruction/image37.png`):
  верхняя полоса (линия, АОН / предоставленный / телефон на место с кнопкой
  АОН, «Происшествие N · Зарег. · Опер.», таймер минут:секунд, красный после
  180 с — только визуальный признак, норматива нет), строка заявителя
  (ФИО, статус, канал связи, иностранный язык), «Пострадавшие: Нет/Есть +
  количество», флаги «Нет на месте / Нет доступа», неактивные «нет контакта /
  срыв звонка», адрес и описание слева; поиск «добавить тип происшествия» с
  выпадающим списком и быстрыми плашками, синие плашки выбранных типов,
  вкладки карт и белые карты 104/101 с тёмным заголовком справа; оранжевая
  панель служб внизу. Выбор служб фиксируется в окне «Добавьте службы»
  (кнопка «+»), × панели возвращает к списку.
- Состояние «не знает» для полей адреса сохранено через компактный переключатель
  «?» у поля; в картах — кнопка «Неизвестно». Команды и API не менялись.
- `web/src/components/Arm112Icons.tsx` — иконки панели. `index.css`: старые
  правила `.intake-profile-case`/`.intake-services` заменены блоком `.arm112`.
  `Workplace.tsx`: для card-only скрыта верхняя кнопка «К списку вызовов»
  (её заменяет × на панели). Рабочее место входящего вызова не менялось.
- `web/e2e/operator112-profiles.spec.ts`: те же проверки через новые
  элементы (поиск типа, плашка «Убрать тип», окно служб) плюс проверки
  `card.incident_types` и отсутствия горизонтальной прокрутки на 1366 px.

Validation: `make verify`, `make verify-web`, `cd web && npm run lint` (один
прежний warning), `cd web && npm run test:e2e` (4 сценария) прошли; ручная
проверка на локальном стеке при 1920 и 1366 px.

Remaining risk: экран входящего вызова (`Operator112Workplace`) ещё в старой
вёрстке. Next: перенести на него те же блоки после подтверждения вида.

## 2026-09-24 — Пересмотр порядка срезов 112 и актуализация CLAUDE.md

Status: план пересмотрен; код не менялся. Часть решений открыта.

- `slice-planning-112.md`: 112-1–112-3 помечены реализованными. Новый порядок:
  112-4 полный кейс (разговор + профильные карты) и отправка нескольким
  службам со снимком на службу → 112-5 ИИ-заявитель и локальный инференс
  (сначала новый ADR вместо ограничения ADR-003 для 112 и замер W0) →
  112-6 оценка 112 (правила + LLM после прохождения) и отчёты → 112-7
  редактор → 112-8 голос → 112-9 приёмка → 112-10 взаимодействие с ДДС
  (ждёт уточнений заказчика). Повторные обращения, обратный звонок,
  intro 112, медицинская/полицейская карты и рекомендации 112 — техдолг.
- Открыты (§3.1 плана): полный или минимальный 112-4 (дополнение после
  отправки), место голосового среза, ёмкость ИИ-заявителя по W0.
- `CLAUDE.md` приведён к текущему состоянию: два типа упражнения, реализованные
  срезы ДДС 1–7 и 112-1–112-3, приоритет `slice-planning-112.md`, карта кода,
  инварианты 112, команды проверок. `AGENTS.md` (неотслеживаемый) не менялся и
  описывает старую нумерацию 112-2…112-9.
- `slice-112-3-plan.md` не менялся: упоминание «взаимодействие с ДДС в 112-4»
  относится к старой нумерации (теперь 112-10).

Validation: `git diff --check`; тесты не запускались (только документация).

Next: решить открытые вопросы §3.1, затем написать `slice-112-4-plan.md`.

## 2026-09-24 — Пересмотр объёма среза 112-4 по инструкции оператора

Status: план изменён; код не менялся.

- Решение пользователя: по инструкции оператора 112 (Рис. 37, 44, 45)
  службы назначаются автоматически по типу, оператор может добавить службу,
  а «Сохранить» → окно «Список оповещаемых служб» → «оповестить и сохранить
  карточку» оповещает весь список одним действием. Отдельный выбор
  адресатов и снимок на каждую службу из прежнего 112-4 убраны.
- `slice-planning-112.md` §7 переписан: комбинированный кейс (разговор +
  профильные карты), единый финал для разговорного маршрута и `card_only`,
  одна неизменяемая запись оповещения (список служб с отметкой
  предложенных/добавленных, один снимок карточки, время, `action_id`),
  чтение старых evidence с одним `dispatch`. Обновлены §3 (таблица,
  причины порядка), §3.1, §9 и §15: вопрос «дополнение после отправки»
  заменён на «повторное сохранение после оповещения» — в инструкции не
  описано, до уточнения карточка закрыта после оповещения, 112-4 не
  блокирует. `CLAUDE.md`: формулировка 112-4 приведена к новому объёму.

Validation: только документация, тесты не запускались.

Remaining risk: повторное сохранение/оповещение после первого оповещения
не уточнено у заказчика. Next: написать `slice-112-4-plan.md`.

## 2026-09-24 — План среза 112-4: единый финал «оповестить и сохранить»

Status: план и ADR написаны; код не менялся.

- `slice-112-4-plan.md`: модель и порядок реализации для 112-4 — новый режим
  сценария `full_case` (разговор + профильные карты), единая команда
  `notify_services {services[], reason}` с общей проверкой списка и причины
  для `card_only`/`full_case`, одна неизменяемая запись `intake_notifications`
  вместо снимка на каждую службу, поле `notification` в evidence рядом с
  существующим `dispatch`, `Finale` в `IntakeState` для выбора маршрута по
  дате создания item, `dispatch_intake` остаётся только для старых
  `incoming_call`. Открытый вопрос — повторное сохранение/оповещение после
  первого `notify_services`, инструкцией не описано, 112-4 не блокирует.
- `design-docs/adr/023-operator112-notify-and-save.md`: решение
  зафиксировано как дополнение к ADR-021 с обоснованием по референсу
  (Рис. 37, 44, 45) и последствиями для evidence и будущей оценки 112-6.

Validation: только документация, тесты не запускались.

Next: миграция и доменные правила `notify_services` для `card_only`
(шаг 2 плана по коммитам), затем `full_case` (шаг 3).

## 2026-09-24 — Срез 112-4, шаг 2: notify_services для card_only

Status: реализовано и проверено; `full_case` (шаг 3) ещё не начат.

- `migrations/00015_operator112_notifications.sql`: таблица
  `intake_notifications` (по образцу `intake_dispatches`, неизменяема) и
  `notify_services` в `actions_type_check`. Обновил
  `internal/platform/postgres/postgres.go`'s `ExpectedSchemaVersion` 14→15 —
  без этого `/readyz` и `migrate ready` никогда не становятся `true` после
  новой миграции (нашёл через ручной прогон `emsim migrate up`/`ready`
  против контейнера postgres, когда интеграционный тест падал на
  "worker exited before readiness" без объяснения причины).
- `internal/training`: `IntakeState.Finale`/`Notified`, `IntakeNotification`,
  `CommandNotifyServices`; `Store.InsertIntakeNotification`/
  `IntakeNotificationByItem` + реализация в `postgres/store.go`; `service.go`
  создаёт card_only item с `Finale="notify"` и читает/пишет notification
  везде, где раньше читался/писался dispatch (Execute, stop-close,
  ItemForTrainee/Instructor).
- `internal/training/operator112/profile_rules.go`: общая
  `validServiceSelection` (вынесена из `review_service_selection`, повторно
  используется `notify_services`); `notify_services`/`complete_intake` для
  `Finale=="notify"`; `review_service_selection`/`complete_profile_case`
  теперь отклоняются для `Finale=="notify"`; `save_intake_draft`/
  `add_incident_type`/`remove_incident_type` отклоняются после `Notified`.
  Уже идущие/закрытые card_only items без ключа `finale` в сохранённом
  `intake_state` (создан до этой миграции) продолжают старым маршрутом без
  какой-либо отдельной миграции данных — отсутствие ключа в jsonb декодируется
  как пустая строка.
- `evidence.go`: поле `Notification` рядом с `Dispatch`.
  `evidence.operator112.schema.json`: `notification` в required, условное
  `service_review` только для `finale!="notify"`, условное `notification`
  для `finale=="notify"`, `dispatch` обязан быть `null` для `card_only`/
  `full_case`. `openapi.yaml`: команда `notify_services`, `Intake112State.
  finale/notified`, схема `Intake112Notification`, `Item.notification`,
  `ItemSummary.notified`.
- Тесты: `profile_rules_test.go` — `TestProfileNotifyServicesFinale` (пустой
  список, служба вне правил, изменение без причины, повтор, правка после
  оповещения, `review_service_selection`/`complete_profile_case` отклонены).
  `test/integration/operator112_profiles_test.go` переписан на новый финал
  (старый `review_service_selection`/`complete_profile_case` явно проверяется
  как отклонённый для новых items) с проверкой `intake_notifications` и
  негативным JSON Schema кейсом без `notification`. `test/integration/
  harness_test.go`: новая таблица добавлена в golden-списки
  `TestPlatformSchema`/`assertApplicationTablesAbsent` и в `resetSchema`
  (иначе `DROP TABLE actions` падает на FK от `intake_notifications`).

Validation: `gofmt`, `make verify`, `make verify-web`,
`python3 design-docs/contracts/check.py`, `go test -tags=integration
./test/integration/...` (полный пакет, включая ДДС/112/worker/recovery) —
все прошли.

Remaining risk: `design-docs/contracts/schema.sql` не обновлён — этот файл
уже отставал от миграций 00012–00014 до начала этой работы (последний раз
трогался на 00011); не стал наверстывать три слоя старого долга внутри
этого среза, чтобы не размывать изменение. `full_case` (объединённый
разговор + профильные карты, шаг 3 плана) ещё не реализован.

Next: `full_case` — режим сценария, объединяющий диалог 112-2 и профильные
карты 112-3, с тем же финалом `notify_services` (шаг 3 плана по коммитам,
`slice-112-4-plan.md`).

## 2026-09-24 — Срез 112-4, шаг 3: full_case (разговор + профильные карты)

Status: реализовано и проверено сквозным интеграционным тестом.

- `internal/training/operator112/rules.go`/`profile_rules.go`: единый
  диспетчер `Decide` теперь направляет и `card_only`, и `full_case` в
  `decideProfileFlow` (бывшая `decideProfileCase`, стала методом `exercise`
  ради доступа к `e.caller`); прежний incoming_call-код вынесен в
  `decideIncomingCall` без изменения поведения. `decideProfileFlow`
  добавляет телефонные команды (`answer_incoming`, `ask_intake_question`,
  `hold/resume/end_incoming`, `mark_no_contact`, `mark_call_dropped`),
  каждая под явным `state.Mode != "full_case"` отказом — card_only их
  по-прежнему не видит. `save_intake_draft` дополнительно отклоняется при
  `CallStatus == "ringing"`; `complete_intake` для `full_case` требует
  `CallStatus == "ended"` вместо `not_applicable`. `dispatch_intake`
  отклоняется для обоих новых режимов через общий `default` (не отдельная
  ветка). Транскрипт/`AskedQuestionIDs` теперь копируются в общем месте
  для card_only и full_case (раньше — только в incoming_call-пути).
- `internal/content`: режим `intake112.mode="full_case"` — диалог 112-2 +
  типы/карты 112-3 в одном сценарии, без `recipient_services`/
  `expected_card`/`recipient_service`; новое `reference.expected_services`
  (закрытый эталон списка служб для ручного разбора, коды проверяются
  против `catalog.Service`). `scenario.schema.json`: четвёртая ветка
  `oneOf` для `full_case`, `expected_services` в `reference.properties`.
- `internal/training/service.go`: `needsCatalog` и создание item расширены
  на `full_case` — каталог обязателен так же, как для `card_only`, но
  `CallStatus` остаётся `"ringing"` (не переопределяется в
  `"not_applicable"|)`; `Finale="notify"` ставится для обоих режимов.
- `evidence.operator112.schema.json`: `intake_state.mode` расширен до
  `["card_only","full_case"]` (было `const "card_only"` — недосмотр шага 2,
  исправлен сейчас); условие «dispatch обязан быть null» тоже расширено.
- `http/handlers.go`: проекции `traineeIntakeState` (скрытие каталога) и
  название типа происшествия в списке карточек учитывают `full_case`
  наравне с `card_only`.
- Seed: `seed/scenarios/pilot-112-full-gas-road-traffic-fire-01.json` —
  разговор про запах газа у ДТП, комбинированный тип
  `gas_explosion_road_traffic_fire`, эталон служб `pilot_gas_104`+
  `pilot_fire_101`. Обновлены жёстко заданные количества сценариев в
  `test/integration/content_import_test.go` (10→11 сценариев, 11→12
  версий, фильтр по `operator112_intake` 6→7) и `api_process_test.go`
  (список сценариев инструктора 10→11).
- Тесты: `internal/content/validate_test.go` — `TestValidateFullCase`
  (10 негативных подслучаев + позитивный); `operator112/profile_rules_test.go`
  — `TestFullCaseFlow` (полный маршрут: звонок → вопросы → типы → карты →
  `notify_services` → отказ `complete_intake` до конца разговора →
  `end_incoming` → `complete_intake`; `dispatch_intake` отклонён). Новый
  `test/integration/operator112_full_case_test.go` —
  `TestOperator112FullCase`: полный кейс через API/БД с проверкой
  `intake_notifications`/`intake_dispatches`, evidence по JSON Schema и
  содержимому (2 карты, транскрипт из 7 реплик, `dispatch=nil`,
  `notification` с двумя службами).

Validation: `gofmt`, `make verify`, `make verify-web`, `python3
design-docs/contracts/check.py`, `internal/content.TestSeedFilesAreValid`,
полный `go test -tags=integration ./test/integration/...` (включая новый
и все существующие 112/ДДС/worker/recovery тесты) — все прошли.

Next: UI — сначала card_only (шаг 4 плана: окно «оповестить и сохранить»
в `Operator112ProfileCase.tsx`), затем перенос экрана входящего вызова на
вёрстку карточки 112-3 для `full_case` (шаг 5).

## 2026-09-24 — Срез 112-4, шаг 4: окно оповещения в card_only UI

Status: реализовано и проверено e2e.

- `web/src/routes/trainee/Operator112Workplace.tsx`: общие TS-типы
  расширены под бэкенд шагов 2-3 — `IntakeState.mode` включает
  `"full_case"`, добавлены `finale?`/`notified`; новый тип
  `IntakeNotification`/`IntakeNotificationService`; `IntakeItem.notification`.
- `web/src/api/commands.ts`: `Command["type"]` дополнен `notify_services`
  (контракт уже нёс его с шага 2, клиентский union — нет).
- `web/src/routes/trainee/Operator112ProfileCase.tsx`: для
  `intake_state.finale === "notify"` окно служб («+») теперь называется
  «Список оповещаемых служб», подтверждение — «оповестить и сохранить
  карточку» (`notify_services`) вместо «Сохранить и закрыть»
  (`review_service_selection`), добавлена кнопка «вернуться к заполнению»
  (закрывает окно без команды). После `state.notified` карточка становится
  read-only (новая переменная `opened` отделяет «кейс открыт» от
  «поля редактируемы», иначе форма ошибочно схлопывалась обратно в экран
  «Открыть кейс» — поймано первым прогоном e2e), кнопка «завершить»
  остаётся доступной и вызывает `complete_intake` вместо
  `complete_profile_case`. Полоса служб показывает «оповещено · время».
  Старые items без `finale` (начатые до среза) не затронуты — используют
  прежние `review_service_selection`/`complete_profile_case`.
- `web/src/index.css`: `.arm112-modal-actions`/`.arm112-modal-cancel` для
  новой кнопки возврата рядом с существующей `.arm112-modal-save`.
- `web/e2e/operator112-profiles.spec.ts`: все три кейса (104, 101,
  комбинированный) проходят через новое окно «Список оповещаемых служб» →
  «оповестить и сохранить карточку»; добавлена проверка
  `item.notification.services` на инструкторской выдаче item.

Validation: `make verify`, `make verify-web`, `cd web && npm run lint`
(единственное предупреждение — предсуществующий `set-state-in-effect` на
несвязанной строке, проверено `git stash` до этой правки), `cd web && npm
run test:e2e` (все 4 сценария, включая обновлённый) — все прошли.

Remaining risk: экран входящего вызова (`Operator112Workplace.tsx`,
`full_case`) всё ещё рендерится старой вёрсткой `incoming_call` (шаг 5 не
начат) — там `dispatch_intake` будет отклонён сервером при попытке
использовать старую кнопку отправки; ручного смоук-теста этого пути пока
не делал, полагаюсь на серверный `transition_not_allowed`.

Next: перенос экрана входящего вызова на вёрстку карточки 112-3 для
`full_case` (шаг 5 плана по коммитам).

## 2026-09-24 — Срез 112-4, шаг 5: full_case на вёрстке карточки 112-3

Status: реализовано и проверено e2e.

- `web/src/routes/trainee/Operator112ProfileCase.tsx`: компонент теперь
  обслуживает и `card_only`, и `full_case` (вместо отдельного дублирующего
  экрана) — новая переменная `isCall = state.mode === "full_case"`
  переключает: заголовок показывает реальное состояние звонка
  (входящий/разговор/на удержании/завершён) с кнопками
  ответить/удержать/вернуться/завершить разговор вместо статичной строки
  «не подключен»; строка «нет контакта»/«срыв звонка»
  (`intake-outcome-controls`, диалог подтверждения `mark_no_contact`/
  `mark_call_dropped` — переиспользованы классы `.intake-outcome-*` из
  старого `Operator112IncomingWorkplace`, они не были оформлены как
  специфичные для `.intake-workplace`); панель разговора и уточняющих
  вопросов (`.intake-transcript`/`.intake-questions`, тоже переиспользованы
  без копирования). Карточка перед ответом на вызов показывает отдельный
  экран ожидания вместо формы. `editable`/`opened` разделены на `opened`
  (кейс открыт) и `bodyReady`/`editable` (форма реально доступна — сверх
  `opened` учитывает, что звонок ещё не принят), иначе шаг 4 уже один раз
  ловил регрессию, когда общее булево значение схлопывало форму обратно в
  экран "Открыть кейс".
- `Operator112Workplace.tsx`: маршрутизация `Operator112Workplace`
  направляет и `card_only`, и `full_case` в `Operator112ProfileCase`;
  `Operator112IncomingWorkplace` (старый `incoming_call`, один
  `dispatch_intake`) не тронут.
- `Workplace.tsx`: `isCardOnly()` (гейт внешней кнопки «← К списку», т.к.
  `Operator112ProfileCase` уже рисует свою) переименован по смыслу и
  расширен на `full_case`.
- `web/e2e/operator112.spec.ts`: новый сквозной тест «full case» — вызов →
  3 уточняющих вопроса → комбинированный тип → 2 профильные карты →
  «Список оповещаемых служб» → «оповестить и сохранить карточку» (104+101,
  без 03 — медицинское поле в карте 101 не отмечено) → завершение разговора
  → «Завершить кейс»; старые тесты (`incoming_call`/dispatch,
  card_only-профили, no_contact/call_dropped) не изменены и проходят.

Validation: `make verify`, `make verify-web`, `cd web && npm run lint`
(тот же предсуществующий `set-state-in-effect`, не мой), `cd web && npm run
test:e2e` — все 5 сценариев (включая новый) прошли с первого прогона.

Remaining risk: визуальная проработка минимальна — панель разговора
использует старые `.intake-transcript`/`.intake-questions` без выделенной
`.arm112-*` стилизации (просто помещена в `.arm112-panel`), а заголовок
звонка получает дополнительную строку `.intake-outcome-controls` через
`grid-column: 1 / -1` внутри `.arm112-top` — работает, но не проходило
пиксельную сверку с инструкцией оператора; это не блокирует функциональность
(DoD среза), но стоит показать эксперту при профильной приёмке вместе с
картами 104/101.

Next: экран преподавателя — показать запись оповещения (`item.notification`)
в мониторе и разборе `instructor/LessonDetail.tsx` (шаг 6 плана по
коммитам).

## 2026-09-24 — Срез 112-4, шаг 6: оповещение служб в мониторе и разборе

Status: реализовано и проверено e2e.

- `web/src/routes/instructor/Monitor.tsx`: строка карточки в мониторе
  теперь добавляет тип происшествия и «· оповещено» и для `card_only`, и
  для звонковых режимов (было только `dispatched`/«· направлена», что для
  нового финала всегда false); заодно исправлен пропущенный статус
  `held` в тексте («на удержании» раньше показывался как «разговор
  завершён»). `commandLabels` дополнен `notify_services` и уже
  существовавшими, но не подписанными командами звонка/опроса
  (`hold_incoming`, `resume_incoming`, `ask_intake_question`,
  `mark_no_contact`, `mark_call_dropped`) — они появляются в «последнем
  действии» строки монитора для `full_case`.
- `web/src/routes/instructor/ItemReview.tsx`: `full_case` теперь тоже
  маршрутизируется в `IntakeProfileReviewPanel` (раньше туда шёл только
  `card_only`, а `full_case` без изменений попал бы в ветку для
  `incoming_call`, рассчитанную на одиночный `dispatch`). Панель получила
  `isCall`-развилку: заголовок «Кейс с разговором»/«Кейс без разговора»,
  секция «Разговор с заявителем» (транскрипт) при `isCall`, подписи для
  звонковых команд и `notify_services`/`complete_intake`/
  `mark_no_contact`/`mark_call_dropped` в логе действий. Раздел «Предложение
  и итоговый выбор служб» показывает `evidence.notification ??
  item.notification` (адресаты, предложенные/добавленные вручную,
  причина, время), если он есть — старый `service_review` остаётся
  фоллбэком для незавершённых до этого среза `card_only`-прохождений.
- Контракты не менялись: `Item.notification`/`ItemSummary.notified` уже
  были добавлены в openapi.yaml шагом 2.
- `web/e2e/operator112-profiles.spec.ts`: разбор комбинированного кейса
  дополнен проверкой строки «Оповещены: …» (все три службы, порядок не
  фиксируется). `web/e2e/operator112.spec.ts`: новый тест шага 5 дополнен
  входом преподавателя на `/instructor/items/{id}/review` — заголовок
  «Кейс с разговором», первая реплика заявителя, строка «Оповещены:
  pilot_gas_104, pilot_fire_101».

Validation: `make verify`, `make verify-web`, `cd web && npm run lint`
(тот же предсуществующий `set-state-in-effect`), `cd web && npm run
test:e2e` — все 5 сценариев прошли.

Next: документация среза 112-4 — README.md, web/README.md,
seed/README.md, статус в slice-planning-112.md/CLAUDE.md (шаг 7 плана по
коммитам).

## 2026-09-24 — Срез 112-4, шаг 7: документация

Status: срез 112-4 (§7 slice-planning-112.md, slice-112-4-plan.md, ADR-023)
реализован по всем 7 шагам плана коммитов; документация обновлена под
фактическое поведение.

- `README.md`: абзац про 112-3 переписан под реальный финал —
  «Список оповещаемых служб» → «оповестить и сохранить карточку»
  (`notify_services`) вместо «Добавьте службы» → «Сохранить и закрыть»
  (`review_service_selection`); добавлен абзац про комбинированный
  `full_case`-кейс (`pilot-112-full-gas-road-traffic-fire-01`): разговор +
  карты в одном задании, тот же финал, старые card_only/incoming_call
  прохождения не затронуты; ссылка на `slice-112-4-plan.md` добавлена к
  списку планов; строка про `npm run test:e2e` упоминает новый сценарий.
- `web/README.md`: описание клиента разделяет legacy `incoming_call`
  (`Operator112IncomingWorkplace`, один `dispatch_intake`) и
  `card_only`/`full_case` (общий `Operator112ProfileCase.tsx` на вёрстке
  АРМ-112, финал `notify_services`/`complete_intake` для items с
  `finale=notify`, старый маршрут — для уже начатых до среза items);
  добавлено, что показывает `ItemReview.tsx` (транскрипт, карты, запись
  оповещения или легаси dispatch/service_review) и `Monitor.tsx` (тип
  происшествия и «· оповещено» в строке активной карточки).
- `seed/README.md`: у существующих `card_only`-сценариев указан новый
  финал; добавлено описание `pilot-112-full-gas-road-traffic-fire-01`
  (`full_case`, эталон `expected_types`/`expected_services`, без
  `recipient_services`).
- `slice-planning-112.md`: статус в шапке и §3 112-4 — «реализован; ждёт
  профильной приёмки» (по образцу 112-3); §7 получил статус-строку; таблица
  порядка среза 112-5 больше не показывает 112-4 как план.
- `CLAUDE.md`: «Current delivery state» перечисляет 112-4 среди
  реализованных срезов, уточняет, что уже начатые до среза items сохраняют
  прежний маршрут, и убирает 112-4 из списка «remaining order».

Validation: `make verify`, `python3 design-docs/contracts/check.py`
(документация не меняла контракты — прогнан для общей уверенности после
серии коммитов среза), `git diff --check`.

Срез 112-4 (шаги 0-7 плана `/Users/lucifer/.claude/plans/4-greedy-journal.md`)
завершён семью коммитами (в дополнение к трём коммитам шага 0, фиксировавшим
уже готовую работу): план/ADR, backend card_only, backend full_case, UI
card_only, UI full_case, инструкторский UI, документация. Открытые риски —
профильная приёмка карт 104/101 и правил служб (унаследовано от 112-3);
повторное сохранение/повторное оповещение после `notify_services` не
реализовано (открытый вопрос §3.1); визуальная проработка панели разговора
на вёрстке 112-3 минимальна (см. запись шага 5). Следующий шаг по плану
112 — 112-5 (ИИ-заявитель), с обязательным новым ADR вместо ограничения
ADR-003 и замером W0.

## 2026-09-24 — Срез 112-5a, коммит 0: план, ADR-024, разделение 112-5

Status: начало реализации 112-5a (§8 slice-planning-112.md, план
`/Users/lucifer/.claude/plans/playful-snacking-quiche.md`). По решению
пользователя срез 112-5 разделён на 112-5a (чат с заявителем, свободный
текст, детерминированная заглушка) и 112-5b (замена заглушки на ИИ,
которую блокирует ещё не сделанный выбор модели и замер W0). Разделение
целесообразно только если заглушка ведёт себя как будущая модель —
отвечает асинхронно, вне транзакции команды, с возможным отказом/таймаутом
— иначе 112-5b пришлось бы заново проектировать протокол хода.

- `design-docs/adr/024-operator112-async-caller-reply.md` (новый):
  асинхронный протокол хода `send_caller_message` → задача `caller.reply`
  в отдельном пуле worker → `ApplyCallerReply` в короткой транзакции.
  Явно фиксирует: ответ заявителя **не увеличивает `items.seq`** (это не
  эффект команды оператора, а отдельный субъект — тот же принцип, что и у
  `item_events` в ДДС); отмену ожидающего хода при
  hold/end/mark_call_dropped/complete_intake; барьер stop (ход остаётся
  `pending` в evidence, ответ после барьера не применяется); отказ через
  общий `Finalizer` при исчерпании попыток; запись `adapter` в каждом
  применённом ходе. Уточняет ADR-022 (синхронный порт `CallerSimulator`
  для `prepared`-диалога остаётся без изменений).
- `slice-112-5a-plan.md` (новый): пользовательский результат, модель и
  правила (`caller_mode`, `send_caller_message`, `caller.reply`, отмена и
  отказ хода, хранение внутри существующего `items.intake_state`),
  Definition of Done, объём вне среза.
- `slice-planning-112.md`: §1 (рабочие допущения), §3 (карта поставки —
  строка 112-5 заменена на 112-5a/112-5b с зависимостями и ссылкой на
  ADR-024, обновлены зависимости 112-6/112-8 на 112-5b), §8 (заголовок и
  вступление объясняют разделение и то, что 112-5b меняет только
  реализацию `CallerReplier`, не протокол).

Validation: перечитаны ADR-022/003, `internal/training/operator112/*`
(caller.go, rules.go, profile_rules.go, evidence.go), `internal/training/
service.go` (Execute/recordDecision/lockForCommand/tickEvent — как
существующий код меняет seq/log_seq без блокировки lessons FOR UPDATE, по
образцу для caller.reply), `platform/tasks` (Spec/Registry/Finalizer),
`cmd/emsim/worker_composition.go` (lessonCloseHandler как образец для
будущего обработчика caller.reply), схема БД (`actions_type_check`,
`items_intake_state_shape`), seed-сценарии (`pilot-112-medical-01-v2`,
`pilot-112-full-gas-road-traffic-fire-01`), `intake-catalog.json`. Только
документация в этом коммите — код не менялся, `make verify` не требуется
для чистого diff в `.md`; будет прогнан вместе с шагом 1.

Next: коммит 1 — домен (`caller_mode`, `send_caller_message`,
`IntakeCallerTurn`, миграция `actions_type_check`, контракты, тесты
правил) без ещё постановки задачи `caller.reply`.

## 2026-09-24 — Срез 112-5a, коммит 1: домен — caller_mode и send_caller_message

Status: домен готов (без постановки задачи caller.reply — она в коммите 2).
Реализует `slice-112-5a-plan.md` §2 «caller_mode сценария»/«новая команда».

- `internal/content/body.go`, `validate.go`: `Intake112.CallerMode`
  (`""`/`"prepared"` | `"free_text"`), допустим только при
  `mode=full_case`; `free_text` требует `dialogue.facts`, запрещает
  `initial`/`questions` (`validateIntake112FreeTextDialogue` — своя
  проверка без reveal-механики, в отличие от `validateIntake112Dialogue`).
  Тесты: `TestValidateFreeTextFullCase`,
  `TestValidateRejectsCallerModeOutsideFullCase`.
- `design-docs/contracts/scenario.schema.json`: `caller_mode` в
  `properties`; `oneOf` для `intake112` разделён на full_case-prepared
  (`dialogue.required += [initial, questions]`) и full_case-free_text
  (пятая ветка, `dialogue.not.anyOf` запрещает initial/questions);
  `dialogue.required` на верхнем уровне сужен до `[facts]`, ветки
  восстанавливают `initial`/`questions` там, где они нужны;
  `caller_mode: false` в трёх остальных ветках (по образцу
  `dialogue: false`).
- `internal/training/domain.go`: `CommandSendCallerMessage`.
  `internal/training/command.go`: `Decision.CallerTurnRequested *int` —
  Decide остаётся чистой функцией, задачу в очередь коммит 2 поставит по
  этому полю.
  `internal/training/intake.go`: `IntakeCallerTurn`
  (`Turn/OperatorLineID/Status/Adapter/RequestedAt/ResolvedAt/Reason`),
  статусы `CallerTurnPending/Answered/Cancelled/Failed`;
  `IntakeState.CallerMode`/`CallerTurns`.
- `internal/training/service.go`: `startOneAssignment` копирует
  `Intake112.CallerMode` в `intakeState.CallerMode` только для full_case
  (card_only не имеет вызова).
- `internal/training/operator112/rules.go`: `AvailableQuestions`
  возвращает `[]` при `caller_mode=free_text` явной проверкой (не только
  из-за пустого `Questions` в `Available`).
  `profile_rules.go` (`decideProfileFlow`): защитная копия
  `state.CallerTurns` (по образцу Transcript/AskedQuestionIDs);
  `answer_incoming` не произносит сценарную реплику в free_text;
  `ask_intake_question` отклоняется в free_text; новый кейс
  `send_caller_message` (mode=full_case, caller_mode=free_text,
  call_status=connected, нет ожидающего хода, текст 1–500 байт после
  trim) добавляет реплику оператора и ход `pending`, выставляет
  `CallerTurnRequested`; `hold_incoming`/`end_incoming`/
  `mark_call_dropped` отменяют ожидающий ход (`cancelPendingCallerTurn`,
  reason held/ended/dropped) — `complete_intake` не нуждается в
  отдельной отмене: он уже требует `call_status=ended`, которое
  недостижимо мимо end_incoming/mark_call_dropped. Тесты:
  `TestFreeTextCallerChatFlow` (полный цикл: answer без реплики, отказ
  ask_intake_question, пустой/слишком длинный текст, первый ход pending,
  повторное сообщение отклонено, hold отменяет ход, resume и второй ход
  получает номер 2 без переиспользования, end отменяет второй ход, номера
  ходов не переиспользуются), `TestSendCallerMessageRejectedInPreparedMode`,
  `TestSendCallerMessageRejectedForCardOnly`.
- `migrations/00016_operator112_caller_message.sql`: `send_caller_message`
  в `actions_type_check` (по образцу 00011–00015); `caller_turns`/
  `caller_mode` живут в существующем `items.intake_state jsonb`, новых
  таблиц/колонок не потребовалось.
- `design-docs/contracts/openapi.yaml`: `send_caller_message` в перечне
  `Command.type` и отдельная ветка `oneOf` (`payload.text`, 1–500);
  `Intake112State.caller_mode`/`caller_turns`.
  `evidence.operator112.schema.json`: те же два поля в `intake_state`
  (schema без `additionalProperties: false`, но поля описаны явно).
  `web/src/api/schema.d.ts` перегенерирован (`npm run generate:api`; файл
  в `.gitignore`, коммитить нечего).

Validation: `gofmt -l` чисто; `make verify` (format-check, build, test,
vet, staticcheck) — зелёный; `python3 design-docs/contracts/check.py` —
все проверки прошли, включая JSON Schema валидацию исправленного
`scenario.schema.json`. `make test-integration`/`make verify-web` не
запускались — это чисто доменный Go-код и контракты, PostgreSQL/UI не
затронуты.

Next: коммит 2 — порт `CallerReplier`, заглушка на 6 фраз, задача
`caller.reply` (пул `caller`, `EnqueueTx` из `recordDecision` по
`CallerTurnRequested`), `ApplyCallerReply`, обработчик и `Finalizer` в
`cmd/emsim/worker_composition.go`, конфиг `CALLER_*`,
интеграционные тесты.

## 2026-09-24 — Срез 112-5a, коммит 2: асинхронный ответ заявителя через worker

Status: готово, три новых интеграционных теста зелёные, `make verify` и
`python3 design-docs/contracts/check.py` зелёные. Реализует
`slice-112-5a-plan.md` §2 «асинхронный ответ: порт, заглушка, задача
worker» и ADR-024 целиком (первый протокол, доведённый до реального
worker-процесса).

- `internal/training/operator112/caller.go`: порт `CallerReplier`
  (`Adapter() string` — провенанс в ход, `Reply(ctx, CallerReplyRequest)
  (CallerReply, error)`), `CallerReplyRequest{Facts, Transcript, Turn}`.
- `internal/training/operator112/stub_caller.go` (новый):
  `StubCallerReplier{Delay}`, 6 фраз пользователя (см. коммит 0), номер
  фразы = `Turn-1`, за пределами — повтор последней; `Reply` ждёт
  `Delay`, уважая отмену контекста. `stub_caller_test.go`: 4 теста
  (последовательность/повтор, `Turn<1`, отмена контекста, тайминг
  задержки).
- `internal/training/service.go`: `KindCallerReply tasks.Kind =
  "caller.reply"`, `CallerReplyDedupKey`; `enqueueCallerReply` —
  `EnqueueTx` в той же транзакции, что и команда (`recordDecision`,
  после `notify`, только при `decision.CallerTurnRequested != nil`);
  `CallerReplyContext` — снимок фактов/транскрипта без блокировок (для
  worker); `ApplyCallerReply`/`FailCallerTurn` — оба через общий
  `lockPendingCallerTurn` (item без лока → lessons FOR SHARE → items FOR
  UPDATE; проверяет `lessons.state=running`, нет `stop_cutoff_log_seq`,
  item не закрыт/не прерван, `call_status=connected`, ход с этим
  номером всё ещё `pending`) и `writeCallerTurnState` (пишет только
  `IntakeState`, `seq`/`log_seq`/`card`/`reaction` не трогает — ADR-024
  «ответ не увеличивает seq»). `TestCallerChatHoldCancelsPendingTurnAndLateReplyIsANoOp`
  проверяет это напрямую: `ApplyCallerReply` на уже отменённый (`hold`)
  ход — чистый no-op, транскрипт и статус не меняются.
- `internal/platform/config/worker.go`: `CallerConcurrency`,
  `CallerReplyTimeout` (валидируются, `CALLER_CONCURRENCY`/
  `CALLER_REPLY_TIMEOUT`); `CALLER_STUB_DELAY` сознательно вне этой
  структуры — читается напрямую через `os.Getenv` в
  `cmd/emsim/worker_composition.go` (по образцу уже существующих
  `BLOB_ROOT`), так как это настройка конкретно заглушки, а не
  структурный параметр воркера; по умолчанию 1с.
- `cmd/emsim/worker_composition.go`: Spec `training.KindCallerReply`
  (пул `caller`, `MaxAttempts=2`, `Lease=2м`, `RetryBase=200ms`,
  `Priority=100`) в `registerKinds`; `callerReplyHandler` — вызывает
  `CallerReplier.Reply` вне транзакции с таймаутом
  `CallerReplyTimeout`, затем одной транзакцией `ApplyCallerReply` +
  `store.Terminal`; `callerReplyFinalizer.FinalizeExpired` —
  `FailCallerTurn` + `FinalizeExpiredTx` в одной транзакции (тот же
  паттерн, что `assessment.Service.FinalizeExpired`);
  `recoveryStore.RegisterFinalizer(training.KindCallerReply, …)`
  регистрируется в `compose()` безусловно (не только внутри
  `composePools`), так как Reaper обслуживающего процесса без роли
  worker тоже должен уметь финализировать эту задачу; пул `caller`
  добавлен в список пулов `composePools`.
- `design-docs/contracts/tasks.schema.json`/`check.py`: пример
  `caller.reply` (`payload.item_id`/`turn`).
- `internal/platform/postgres/postgres.go`: **исправлена забытая с
  коммита 1 регрессия** — `ExpectedSchemaVersion` оставался `15` после
  добавления `migrations/00016_operator112_caller_message.sql`, из-за
  чего `Ready()` после применения всех 16 миграций всегда возвращал
  `false` и любой процесс `emsim` падал на старте с «database schema is
  not ready». Значение исправлено на `16`. Обнаружено интеграционными
  тестами этого коммита (первыми, что реально стартуют `emsim worker`
  подпроцессом после коммита 1); не было замечено раньше, так как
  `make verify` не поднимает БД, а `make test-integration` для коммита 1
  не запускался (домен без интеграции с очередью).
- `test/integration/operator112_caller_chat_test.go` (новый):
  `TestCallerChatAsyncReplySequence` (реальный `emsim worker`
  подпроцессом, 8 ходов подряд — 6 фраз по порядку, затем повтор
  последней дважды; после каждого хода `items.seq` сверяется с тем, что
  было сразу после **собственной** команды оператора
  `send_caller_message`, а не с 0 — сама команда оператора seq
  увеличивает как обычно принятая команда, ADR-024 запрещает только
  дополнительный рост от асинхронного ответа заявителя),
  `TestCallerChatHoldCancelsPendingTurnAndLateReplyIsANoOp` (без
  worker — hold отменяет ход детерминированно, поздний прямой вызов
  `ApplyCallerReply` — no-op), `TestCallerChatFinalizerFailsExhaustedTurn`
  (таймаут короче задержки заглушки — обе попытки истекают, ход
  становится `CallerTurnFailed`, чат принимает новое сообщение сразу
  после).
- `test/integration/training_service_test.go`: у теста свой локальный
  `newTrainingService`/`mustTaskEnqueuer` с отдельным `tasks.Registry` —
  добавлена регистрация `training.KindCallerReply`, иначе
  `send_caller_message` там падал с «task kind is not registered».

Два бага нашлись и исправлены только на интеграционных тестах с реальным
worker-процессом (`go build ./cmd/emsim` + `exec.Command`), не на
доменных unit-тестах:

1. `callerReplyHandler` изначально возвращал ошибку `CallerReplier.Reply`
   как есть. `internal/platform/tasks/runner.go` считает такую
   («неклассифицированную») ошибку не бизнес-неполадкой, а
   операционным багом и **останавливает весь процесс worker**
   (`ErrOperational`) — это документированное поведение
   (`failure.go`: «an unexpected error, which the Runner treats as an
   operational failure and stops on»), а не баг очереди. Таймаут
   заглушки — ожидаемый, штатно повторяемый отказ, поэтому исправлено:
   ошибка от `Reply` оборачивается в
   `tasks.NewHandlerFailure(tasks.Retryable, "caller_reply_unavailable")`.
   Это первый обработчик в кодовой базе, вызывающий внешний адаптер с
   реальной задержкой вне транзакции (docs/comment в
   `callerReplyHandler`), поэтому и первый, где это разграничение вообще
   имело значение — `noopHandler`/`lessonCloseHandler` его не делают
   осознанно: их ошибки (сбой транзакции/БД) всегда операционные, для
   них крах и рестарт процесса — верное поведение.
2. Исчерпание `MaxAttempts=2` с классификацией `Retryable` доходит до
   `finalizeResolvedFailure`, которая для `Retryable` считает терминальный
   статус задачи `dead_letter`, а не `failed` (`failed` зарезервирован за
   `Permanent`) — `callerReplyFinalizer` при этом отрабатывает одинаково
   в обоих случаях. Тест ждал буквально `status='failed'`; поправлен на
   `status IN ('failed','dead_letter')`.

Validation: `gofmt -l` чисто; `make verify` зелёный; `python3
design-docs/contracts/check.py` зелёный; `make test-integration`
(весь пакет `test/integration`, не только новые тесты) — зелёный,
164с.

Riски/что дальше: `CALLER_STUB_DELAY` по умолчанию 1с — в
`docker compose up` реальная задержка ответа будет заметна, это
намеренно (RFC «заявитель отвечает не мгновенно»). Коммит 3 — тестовый
сценарий `free_text` для сидирования (сейчас есть только одноразовая
JSON-фикстура внутри теста коммита 2, не в `seed/`).

Next: коммит 3 — `seed/scenarios/pilot-112-free-text-chat-01.json`
(`full_case`/`caller_mode: free_text`, газ+ДТП → 104/101, как у
`pilot-112-full-gas-road-traffic-fire-01`), `seed/README.md`.

## 2026-09-24 — Срез 112-5a, коммит 3: сидируемый сценарий для чата

Status: готово. Реализует `slice-112-5a-plan.md` §3 «Сценарий для чата».

- `seed/scenarios/pilot-112-free-text-chat-01.json` (новый): та же
  карточка/эталон, что у `pilot-112-full-gas-road-traffic-fire-01.json`
  (запах газа рядом с ДТП, `expected_types:
  ["gas_explosion_road_traffic_fire"]`, `expected_services:
  ["pilot_gas_104","pilot_fire_101"]`), но `intake112.caller_mode:
  "free_text"` и `dialogue` содержит только `facts` — без
  `initial`/`questions` (запрещено `validateIntake112FreeTextDialogue`,
  `internal/content/validate.go:169`). `reference.case_description`
  явно объясняет, что фразы заглушки `StubCallerReplier` не связаны с
  фактами этого кейса и учебной ценности разговором пока не несут — она
  появится в 112-5b с настоящей моделью.
- `seed/README.md`: описание нового файла рядом с описанием
  `pilot-112-full-gas-road-traffic-fire-01.json`.
- Три места хардкодили число сидируемых сценариев/версий и потребовали
  правки вместе с добавлением тринадцатого файла (12-й ключ):
  `test/integration/content_import_test.go` (`ImportScenarios`:
  `NewScenarios 11→12`, `NewVersions 12→13`, повторный импорт `Unchanged
  12→13`; `ListScenarios{}`: `total/len 11→12`; `ListScenarios{ExerciseType:
  operator112_intake}`: `total/len 7→8`), `test/integration/api_process_test.go`
  (`GET /api/v1/scenarios`: `Total/len(Items) 11→12`).

Validation: новый файл сначала проверен доменным unit-тестом
`internal/content.TestSeedFilesAreValid` (валидирует каждый файл в
`seed/scenarios` через `content.Validate`, без БД) — прошёл сразу;
`python3 design-docs/contracts/check.py` — зелёный; `make verify` —
зелёный; `make test-integration` — весь пакет `test/integration`
целиком (не только import-тесты), зелёный, ~163с — подтверждает, что
все три поправленных хардкода теперь совпадают с фактическим числом
сидируемых сценариев и версий.

Next: коммит 4 — trainee UI, плавающее окно чата
(`web/src/routes/trainee/CallerChat.tsx`), кнопка «Чат с заявителем» в
`Operator112ProfileCase.tsx`, замена секции «Разговор с заявителем» на
окно для `free_text`-кейсов, и попутное исправление сброса
несохранённого черновика карточки (`useEffect(..., [item.seq])` →
`[item.card]`), которое иначе стирает текст, набранный в чате.

## 2026-09-24 — Срез 112-5a, коммит 4: trainee UI — плавающее окно чата

Status: готово, вручную пройден сквозной сценарий через
`docker compose up --build` (реальный worker + заглушка), существующие
Playwright e2e не сломаны (5/5 зелёных). Реализует
`slice-112-5a-plan.md` §4 «UI обучаемого: окно чата».

- `web/src/routes/trainee/CallerChat.tsx` (новый): плавающее окно
  `position: fixed` поверх карточки (правый нижний угол), не встроенное
  в форму — именно то, что требовал исходный пользовательский запрос
  («не инкорпорировано в карточку», «как окно с чат-ботом на сайте»).
  Свёрнутое состояние — круглый лаунчер со счётчиком непрочитанных
  реплик заявителя; `open`/`onToggle` подняты в проп из
  `Operator112ProfileCase`, чтобы и кнопка в `arm112-line`, и заголовок
  самого окна управляли одним состоянием. Реплики — пузыри (оператор
  справа/синий, заявитель слева/белый, `white-space: pre-line` для
  переноса строки в пятой фразе заглушки). Индикатор «Заявитель
  печатает…» пока последний ход `pending`; при `failed` — «не ответил
  (техническая причина) — повторите сообщение», ввод по-прежнему
  доступен. Ввод заблокирован, пока `call_status!==connected`, есть
  ожидающий ход или есть любая другая неподтверждённая команда карточки
  (`pending`, тот же глобальный гейт, что и у остальных команд
  карточки). Enter отправляет, Shift+Enter — перенос, счётчик 0/500.
  Счётчик непрочитанных — паттерн «adjust state during render»
  (react.dev), не эффект: `readCount` подстраивается под `callerLines`
  всегда, пока окно открыто, а не только в момент открытия — иначе
  реплика, пришедшая при уже открытом окне, после сворачивания ошибочно
  считалась бы непрочитанной (поймано и исправлено вручную в браузере
  до коммита, см. ниже).
- `Operator112ProfileCase.tsx`: состояние `chatOpen` с восстановлением
  из `localStorage` (`emsim:112:chat-open:<item.id>`, в try/catch, по
  умолчанию открыто при первом упоминании); кнопка «чат с заявителем»/
  «скрыть чат» в `arm112-line` рядом с «удержать»/«завершить разговор»;
  секция `<h3>Разговор с заявителем</h3>` теперь рендерится только при
  `state.caller_mode !== "free_text"` — для чата её полностью заменяет
  `<CallerChat>` в самом низу дерева (не скрыта, а не рендерится вовсе,
  чтобы дважды не показывать транскрипт).
  **Исправлен сброс черновика** (был запланирован ещё при разборе
  среза): `useEffect(() => { setDraft(item.card); resetSelection(); },
  [item.seq])` перезаписывал несохранённые поля карточки после ЛЮБОЙ
  принятой команды, включая `send_caller_message` — то есть отправка
  сообщения в чат стирала недописанный адрес. Заменено на
  `[cardSignature]`, где `cardSignature = JSON.stringify(item.card)` —
  не `item.card` по ссылке: `/my/stream`'s SSE-обработчик
  (`Workplace.tsx`) инвалидирует `itemQueryKey` на любое уведомление,
  включая асинхронный ответ заявителя (сам `writeCallerTurnState`
  вызывает `notify`), так что каждый такой рефетч создаёт новый объект
  `item.card` по ссылке с тем же содержимым — сравнение по ссылке было
  бы тем же самым багом на новом основании. `dirty` в этом же файле уже
  сравнивал именно так (`JSON.stringify`); теперь оба места используют
  один `cardSignature`, посчитанный один раз за рендер.
- `Operator112Workplace.tsx`: `IntakeLine` экспортирован (был локальным
  типом); новый `IntakeCallerTurn` (`turn/operator_line_id/status/
  adapter/requested_at/resolved_at/reason`); `IntakeState` получил
  `caller_mode`/`caller_turns`.
- `web/src/api/commands.ts`: `"send_caller_message"` в объединении
  типов `Command["type"]`.
- `web/src/api/schema.d.ts` перегенерирован (`npm run generate:api`,
  gitignored) — подтвердил, что `openapi.yaml` из коммита 1 уже несёт
  нужные `caller_mode`/`caller_turns`/`send_caller_message.payload.text`
  без изменений контракта в этом коммите.
- `web/src/index.css`: `.caller-chat*`/`.caller-chat-launcher`/
  `.caller-chat-badge` — новый блок стилей рядом с `.arm112-modal*`,
  свои токены цвета переиспользует из существующих `--a112-*`
  переменных; окно `z-index:20` (выше липкой `.arm112-bar` с `z-index:6`,
  ниже модалки служб с `z-index:30`, чтобы модалка перекрывала чат, если
  оба открыты одновременно).

Один баг пойман и исправлен в процессе ручной проверки, до коммита (не
в юнит- или e2e-тестах — счётчик непрочитанных нигде больше не
проверяется автоматически): первая версия счётчика обновляла
`readCount` только в момент перехода `open: false → true`
(`useState`+сравнение с `prevOpen`), поэтому реплики, пришедшие, пока
окно уже было открыто, никогда не попадали в `readCount` — после
сворачивания badge показывал их все как непрочитанные (воспроизведено:
открыл чат, получил 2 ответа, свернул — badge показал «2» вместо «0»).
Исправлено на «синхронизировать `readCount` с `callerLines` в любой
момент, когда `open===true`», не только на границе перехода.

Ручная проверка (`docker compose up --build`, реальный `emsim worker` +
`StubCallerReplier`, `CALLER_STUB_DELAY` по умолчанию 1с):
1. Преподаватель назначил `pilot-112-free-text-chat-01` (коммит 3) на
   РМ-07 обучаемому; занятие запущено.
2. Обучаемый принял вызов — окно чата открылось само (без явного клика),
   ровно в момент, когда `bodyReady` стал true.
3. Четыре сообщения подряд получили первые четыре фразы заглушки по
   порядку (проверено текстом реплик в DOM).
4. Несохранённый черновик адреса, набранный между отправками сообщений
   в чат, пережил два асинхронных ответа заявителя (подтверждает
   исправление `cardSignature`).
5. Удержание вызова во время открытого окна корректно блокирует
   textarea (`disabled=true`) и показывает подсказку «Вызов на
   удержании»; возврат к разговору снимает блокировку.
6. Сворачивание/разворачивание чата сохраняет всю историю сообщений;
   состояние открыт/свёрнут восстанавливается из `localStorage` после
   полной перезагрузки страницы (проверено через `docker compose
   up --build` — пересоздание контейнеров, `item_id` тот же).
7. `state.caller_turns` в БД после теста: 4 хода, все `answered`,
   `adapter: "stub/v1"` — совпадает с ADR-024.

Validation: `make verify-web` (`tsc -b` + `vite build`) — зелёный; `cd
web && npm run lint` — только один уже существовавший warning
(`Operator112ProfileCase.tsx:131`, не мой код, тот же паттерн, что и
восстановление pending-команды при монтировании); `cd web && npm run
test:e2e` — существующие 5 сценариев (`operator112-profiles`,
`operator112` ×3, `smoke`) зелёные без изменений — подтверждает
отсутствие регрессии в prepared `full_case`/`card_only`/
`incoming_call`. Новый e2e-сценарий для самого чата — в коммите 5
вместе с остальными изменениями UI преподавателя (по плану).

Next: коммит 5 — instructor UI (`ItemReview.tsx` — статусы ходов и
адаптер в разборе, `Monitor.tsx` — подпись последнего действия для
`send_caller_message`) и новый e2e-сценарий чата в
`web/e2e/operator112.spec.ts`.

## 2026-09-24 — Срез 112-5a, коммит 5: instructor UI и e2e-сценарий чата

Изменённые файлы:

- `web/src/routes/instructor/ItemReview.tsx`: `IntakeProfileReviewPanel`
  (ветка `isCall`) под транскриптом теперь показывает список ходов
  свободного диалога, если `intake_state.caller_turns` не пуст —
  отдельный `<h4>Ходы свободного диалога</h4>` + `<ul>`, а не встраивание
  в сам транскрипт: ход и реплика заявителя не всегда 1:1 (`failed`/
  `cancelled`/зависший `pending` ходы не оставляют реплики), поэтому
  сшивать статус с конкретной строкой транскрипта было бы либо неверно,
  либо потребовало бы `operator_line_id`-сопоставления ради чисто
  косметической правки. `turnStatusText()` — точная формулировка из
  плана: `answered` → «Отвечено (stub/v1)» (адаптер только здесь,
  согласно `openapi.yaml`'s `adapter` — «заполнено только при
  status=answered»), `failed` → «Нет ответа — техническая причина»,
  `cancelled` → «Отменён: <удержание|завершение|срыв звонка>» по
  `reason` (`held`/`ended`/`dropped` — проверено в бэкенде:
  `cancelPendingCallerTurn` в `profile_rules.go`, единственные три
  вызывающих места), `pending` → «Без ответа на момент остановки» (это
  единственный путь, которым ход остаётся `pending` в закрытом evidence
  — `closeInterruptedItem` его не трогает). Типы: новый
  `IntakeReviewCallerTurn`, `IntakeReviewState` (вынесен из дублированной
  инлайн-аннотации `intake_state` у `IntakeReviewItem`/
  `IntakeReviewEvidence`) с `caller_mode`/`caller_turns`. `actionText()`
  получил `send_caller_message` → «Сообщение заявителю».
- `web/src/routes/instructor/Monitor.tsx`: та же подпись «Сообщение
  заявителю» в `commandLabels` для `last_action.type`.
- `web/e2e/operator112.spec.ts`: новый сценарий — «free-text caller
  chat» (назначение `pilot-112-free-text-chat-01` → ответ на вызов →
  окно открывается само → первое сообщение и первая фраза заглушки →
  свернуть чат, отредактировать несохранённый адрес, развернуть чат,
  убедиться, что черновик не сброшен (коммит-4 баг) → фразы 2–6 по
  порядку → 7-е сообщение повторяет 6-ю фразу → удержание во время
  «печатает…» отменяет ход (проверено через API: `status=cancelled`,
  `reason=held`) → возврат к разговору снимает блокировку → сохранить
  карточку → оповестить и сохранить → завершить кейс → в разборе
  преподавателя видно транскрипт, «Ходы свободного диалога» с
  `Отвечено (stub/v1)`/`Отменён: удержание`, подпись действия
  «Сообщение заявителю». Подпись `Monitor.tsx` проверена не через
  UI-навигацию в конце теста (к этому моменту последнее действие —
  уже `complete_intake`, не `send_caller_message` — навигация была бы
  проверкой не того), а отдельным `GET /lessons/{id}/monitor` через
  собственный `APIRequestContext` инструктора сразу после первого
  отправленного сообщения, пока оно действительно последнее.
- `web/e2e/run.mjs`: `CALLER_STUB_DELAY=800ms` для e2e-прогона (вместо
  продовского 1с по умолчанию из `compose.yaml`) — компромисс:
  0 оказался слишком быстрым (см. ниже), 800мс достаточно и для
  наблюдаемого «печатает…», и для надёжного окна на клик «удержать» до
  того, как асинхронный ответ применится, и не растягивает 7
  последовательных ходов теста заметно.
- `compose.yaml`: `CALLER_STUB_DELAY: ${CALLER_STUB_DELAY:-1s}` в
  окружении `worker` — раньше переменная читалась процессом
  (`cmd/emsim/worker_composition.go`'s `callerStubDelay()`, коммит 2), но
  не была проброшена в `docker compose`-сервис, поэтому `web/e2e/run.mjs`
  не мог на неё повлиять. Продовый `docker compose up --build` без
  явного `CALLER_STUB_DELAY` в окружении хоста не меняется (подтверждено
  `docker compose config` — резолвится в `1s`).
- `web/e2e/operator112.spec.ts`, `web/e2e/operator112-profiles.spec.ts`,
  `web/e2e/smoke.spec.ts`: общая проблема, не связанная с самим срезом
  112-5a напрямую, но вскрытая добавлением 6-го теста в файл — все
  e2e-сценарии логинятся под одним и тем же bootstrap-логином `admin`
  (`RFC-001 §9`: 5 попыток/мин на логин, `internal/auth/ratelimit.go`,
  фиксированное окно). При 5 сценариях в сумме (`operator112.spec.ts`×3
  + `operator112-profiles.spec.ts`×1 + `smoke.spec.ts`×1) счётчик уже
  стоял ровно на границе; добавление 6-го (моего) теста на быстрой
  машине с прогретым docker-кешем иногда укладывает все шесть логинов
  `admin` в одно 60-секундное окно, и последний по алфавиту файл
  (`smoke.spec.ts`) получает `429 rate_limited` — не флуктуация теста, а
  реальное столкновение с настоящим security-лимитером (специально не
  ослаблен и не обойдён). Исправлено добавлением локального
  `adminLogin()`/переработкой `login()` в каждом файле — retry с backoff
  5с при 429, до 14 попыток (~70с потолок, с запасом относительно
  фиксированного 60-секундного окна лимитера); при отсутствии
  рейт-лимита retry не срабатывает и не добавляет накладных расходов.
- `web/src/index.css`: `.arm112-line-state div { flex-wrap: wrap; }` +
  `.arm112-line-state button { flex: 1 1 auto; min-width: 64px; }` —
  реальный баг вёрстки, найденный e2e, не сам тест: заголовочная строка
  разговора (`.arm112-line-state`) уже содержала 2 кнопки
  («удержать»/«завершить разговор»); третья («чат с заявителем» из
  коммита 4) переполняла узкую (`minmax(190px, 1.15fr)`) первую колонку
  CSS grid `.arm112-top` без переноса — визуально обрезалась и физически
  перекрывалась следующей ячейкой грида (`.arm112-phone-side`, непрозрачный
  фон, красится позже в DOM-порядке), из-за чего клик по «чат с
  заявителем»/«скрыть чат» не доходил до кнопки (`element intercepts
  pointer events`, воспроизведено полностью реальным Playwright-кликом,
  не догадкой). Единственное использование `.arm112-line-state` в кодовой
  базе — этот компонент, так что правка не задевает
  `Operator112Workplace.tsx`.

Найденные и исправленные в процессе e2e-прогонов баги (не находки
ревью, а фактические падения теста, каждое воспроизведено и
перепроверено после фикса):
1. CSS-перекрытие кнопки «чат с заявителем» соседней колонкой грида
   (см. выше) — реальный UI-баг, не тестовый.
2. Общий rate-limit на bootstrap-логин `admin` при полном прогоне
   набора e2e (см. выше) — инфраструктурная хрупкость тестов, а не
   баг приложения; лимитер не тронут.
3. Два поля карточки в `Operator112ProfileCase.tsx` (`card_only`/
   `full_case` вёрстка) названы не так, как в старом
   `Operator112Workplace.tsx` (`incoming_call`-вёрстка): «Дом/Вл», не
   «Дом»; статус звонка «на удержании» строчными, не «На удержании» —
   тестовая ошибка (скопировано из другого компонента), не баг кода.

Validation: `make verify-web` (`tsc -b` + `vite build`) — зелёный; `cd
web && npm run lint` (включая `e2e/*.spec.ts`, отдельно перепроверено
`npx oxlint e2e/operator112.spec.ts`) — только тот же
предсуществующий warning `Operator112ProfileCase.tsx:131`; e2e-файлы не
входят в `tsc -b`'s `include`, поэтому типы `e2e/operator112.spec.ts`
дополнительно проверены отдельным временным `tsconfig` (`strict: true`)
— чисто; `python3 design-docs/contracts/check.py` не запускался —
контракт не менялся в этом коммите (только UI/e2e/compose); `cd web &&
npm run test:e2e` — полный набор (`operator112-profiles`, `operator112`
×4 считая новый, `smoke`) прогнан дважды подряд без остановки контейнеров
между прогонами (тот самый сценарий, что вскрыл rate-limit-баг) — оба
раза 6/6 зелёных, включая новый сценарий (11.7с) и `smoke.spec.ts`
(28–43с, видна работа retry-логики на границе лимитера).

Next: коммит 6 — финальный документационный проход: `README.md`,
`web/README.md`, `seed/README.md`, статус 112-5a → «реализован» в
`slice-planning-112.md`, «Current delivery state» в `CLAUDE.md`, и
итоговая запись в `LOG.MD` о том, что 112-5a завершён и следующий шаг
— 112-5b (адаптер модели + замер W0).

## 2026-09-24 — Срез 112-5a, коммит 6: документация, срез завершён

Финальный документационный проход по плану (`slice-112-5a-plan.md`),
без изменений в коде/тестах:

- `README.md`: новый абзац про 112-5a сразу после описания 112-4 —
  окно чата, `caller_mode: "free_text"`, асинхронный `caller.reply` вне
  транзакции команды (ссылка на ADR-024), заглушка `stub/v1` и что
  именно заменит 112-5b (только порт `CallerReplier`), инвариант
  «ответ не увеличивает `items.seq`», поведение при удержании/
  завершении/stop, что видит преподаватель (статусы ходов и адаптер в
  разборе, подпись в мониторе), переменные окружения
  `CALLER_STUB_DELAY`/`CALLER_CONCURRENCY`/`CALLER_REPLY_TIMEOUT`.
  Список планов среза 112 дополнен ссылкой на `slice-112-5a-plan.md`.
- `web/README.md`: `test:e2e`'s список сценариев в комментарии дополнен
  «112 free-text caller chat»; новый абзац про `CallerChat.tsx` рядом с
  описанием `full_case`-вёрстки — плавающее окно, лаунчер с
  непрочитанными, черновик карточки переживает асинхронный ответ
  именно потому, что сброс черновика завязан на `JSON.stringify
  (item.card)`, а не на `item.seq` (который бампит и сама команда чата);
  индикатор «печатает…»; отмена хода при удержании/завершении/срыве.
  Описание `ItemReview.tsx`/`Monitor.tsx` дополнено статусами ходов,
  адаптером и подписью `send_caller_message` (коммит 5).
- `seed/README.md`: уже содержит запись про
  `pilot-112-free-text-chat-01` с коммита 3 — без изменений в этом
  коммите, проверено `grep`.
- `slice-planning-112.md`: шапка «Статус» — 112-1–112-4 и 112-5a
  реализованы; строка 112-5a в таблице §3 — план → реализован; §8 —
  «план» → «реализован» для 112-5a, «112-5b» явно помечен «план».
- `slice-112-5a-plan.md`: шапка «Статус: план» → «реализован (коммиты
  0–5; коммит 6 — документация)».
- `CLAUDE.md` («Current delivery state» и смежные инварианты): 112-5a
  добавлен в список реализованных срезов с кратким описанием
  (свободный чат, заглушка, ADR-024); «остающийся порядок» теперь
  начинается с 112-5b, а не 112-5; ADR-003-инвариант уточнён — 112-5a
  не звал модель (заглушка за протоколом ADR-024), реальный вызов
  модели всё ещё ждёт отдельного ADR в 112-5b; карта кода про
  Operator112Workplace.tsx/Operator112ProfileCase.tsx поправлена
  (первый — только досрезовые `incoming_call`, не «call-based» вообще;
  второй — и `card_only`, и `full_case`) и дополнена `CallerChat.tsx`.

Validation: документация не меняет исполняемый код — `make verify`
(зелёный, без изменений с коммита 5) и `make verify-web` не запускались
повторно ради этого коммита; проверено вручную, что все новые
markdown-ссылки (`slice-112-5a-plan.md`, `ADR-024`) существуют
(`ls`/`grep` по файлам, см. выше) и что `seed/README.md` не нуждается в
правке. Срез 112-5a (`slice-planning-112.md` §3) закрыт: коммиты 0–6.

Итог среза 112-5a: свободный текстовый чат с заявителем поверх
`full_case`, детерминированная заглушка из шести фраз по кругу,
асинхронный протокол хода (`caller.reply` в отдельном пуле worker, вне
транзакции команды, не увеличивает `items.seq`, отмена при удержании/
завершении/срыве, барьер stop, finalizer при исчерпании попыток) —
полностью реализован и покрыт интеграционными и browser e2e тестами;
разбор преподавателя и монитор показывают ходы, адаптер и действие.
Next: срез 112-5b — заменить `StubCallerReplier` на настоящий локальный
инференс. Первый шаг по плану (`slice-planning-112.md` §8) — отдельный
ADR, явно разрешающий интерактивного заявителя для 112 (ADR-003
сохраняется для остального); второй шаг — замер W0 на целевом сервере
(6 CPU, без GPU): задержка и качество ответа на русском при
concurrency 1/2/4, чтобы выбрать модель и `CALLER_CONCURRENCY`/
`CALLER_REPLY_TIMEOUT` для продакшна (сейчас там демо-заглушка с
фиксированными значениями, W0 не проводился).

## 2026-09-25 — Срез 112-5b, коммит 1: ADR-025 и план этапа 1

Только документы, без изменений в коде/тестах:

- `design-docs/adr/025-operator112-ai-caller.md`: новый ADR — узкое
  уточнение ADR-003 только для `caller.reply` 112 (ДДС и все остальные
  `Kind` — ADR-003 без изменений). Решение: модель — локальный
  OpenAI-совместимый сервис без выхода в интернет; гибрид «правила
  открывают факты — модель формулирует»; модели не передаются эталон,
  рубрика, список служб, закрытые факты; таймаут попытки и
  `MaxAttempts=2` из ADR-024 без изменений; отказ на последней попытке —
  нейтральная реплика вместо `HandlerFailure`; stop/поздний ответ/
  finalizer — по ADR-024 без изменений; каждый ход фиксирует
  `source`/`generation` (RFC-001 §6 распространяется и на реплики
  заявителя); `CALLER_REPLIER=stub` — значение по умолчанию, ёмкость не
  заявлена до W0.
- `design-docs/adr/003-llm-outside-interactive-path.md`: строка статуса
  дополнена ссылкой «для 112 `caller.reply` уточнён ADR-025» — текст
  решения не менялся.
- `design-docs/adr/README.md`: индекс был не синхронизирован (не
  содержал строк 022–024) — добавлены строки 022, 023, 024, 025.
- `slice-112-5b-plan.md`: новый план (формат `slice-112-5a-plan.md`) —
  срез разделён на этап 1 (эта и следующие пять коммитов — ИИ-адаптер
  за портом `CallerReplier`, работает на Ollama в разработке) и этап 2
  (W0 на целевом сервере, `llama-server`/GGUF в compose, ёмкость
  класса — не входит в этот план, отдельная последующая работа). Восемь
  ключевых проектных решений (состояние без сохранения, вступление и
  фиксированный ответ без модели, раскладка промпта под кеш префикса
  llama.cpp, regex-спецслучаи из `caller.py` как данные сценария,
  fallback на нейтральную реплику при отказе, выбор адаптера через
  `CALLER_REPLIER`, воспроизводимость через `source`/`generation`) —
  зафиксированы как план для коммитов c2–c6. Definition of Done этапа 1
  и среза 112-5b (только после этапа 2) — раздельные.

Validation: документация не меняет исполняемый код — сборка/тесты не
запускались ради этого коммита; проверено вручную (`grep`/`ls`), что
все новые markdown-ссылки (`ADR-025`, `slice-112-5b-plan.md`, ссылки на
`ADR-024`/`ADR-022`/`ADR-023` в новых строках README) существуют.

Next: коммит c2 — минимальное расширение схемы сценария `free_text`
профилем заявителя (`Intake112CallerProfile`, поля факта) в
`internal/content`, синхронные обновления контрактов
(`scenario.schema.json`, `openapi.yaml`, `web/src/api/schema.d.ts`).

## 2026-09-25 — Срез 112-5b, коммит 2: профиль заявителя в схеме сценария

Минимальное расширение (вариант «a» из `slice-112-5b-plan.md`) схемы
`free_text`-диалога для будущего ИИ-адаптера, без изменений в
`internal/training`/worker — только `content` и контракты:

- `internal/content/body.go`: `Intake112Dialogue.Caller
  *Intake112CallerProfile{Persona, Opening}`; `Intake112Fact` получил
  `Statement`, `AskPatterns`, `AskExcludePatterns`,
  `AnswerVariants []Intake112AnswerVariant{When, Text}`,
  `DisclosurePatterns` (все `omitempty`); `CardPath` стал `omitempty`
  (у free_text может отсутствовать — нарративный факт без поля
  карточки, например номер машины).
- `internal/content/validate.go`
  (`validateIntake112FreeTextDialogue`): `caller` необязателен —
  сценарии 112-5a без профиля (в т.ч. `pilot-112-free-text-chat-01`)
  остаются валидны; при заданном профиле обязательны `persona` и
  `opening.text`, `opening.reveals` может ссылаться только на
  `initial`-факты, у `initial`/`on_question`-фактов обязателен
  `statement`. `card_path`, если указан, по-прежнему проверяется
  `validIntake112CardPath` и на дубликаты; если не указан — факт
  нарративный (только регэкспы формируют классификацию, значение никуда
  не пишется в карточку). Все регэксп-поля (`ask_patterns`,
  `ask_exclude_patterns`, `disclosure_patterns`,
  `answer_variants[].when`) компилируются как `(?i)`+шаблон при импорте;
  `\b` запрещён (RE2 не работает с кириллицей через `\b`) —
  `validateCallerRegexPattern`. `validateIntake112Dialogue` (prepared)
  отклоняет `dialogue.caller` и любое из новых полей факта —
  `forbidIntake112CallerProfileFields`.
- `design-docs/contracts/scenario.schema.json`: `facts.items.required`
  лишился `card_path` (структурно необязателен везде; required-по-
  caller_mode проверяет только Go `Validate`, как и раньше для похожих
  кросс-полевых правил); добавлены `statement`, `ask_patterns`,
  `ask_exclude_patterns`, `disclosure_patterns`, `answer_variants[]`
  (`when`?, `text`) и `dialogue.caller` (`persona`, `opening`).
- `design-docs/contracts/openapi.yaml`: `Intake112Dialogue` (инструкторская
  проекция) — то же расширение полей факта и `caller`; `card_path` убран
  из `required`.
- `web/src/api/schema.d.ts` перегенерирован (`npm run generate:api`) —
  файл в `.gitignore`, коммитить нечего.
- Тесты: `internal/content/validate_test.go` —
  `TestValidateAICallerProfile` (валидный профиль + 10 подслучаев:
  пустая persona, пустой opening.text, opening.reveals на
  неизвестный/`on_question` факт, отсутствующий statement при заданном
  caller, битый regex, `\b` в `ask_patterns`/`disclosure_patterns`,
  пустой text у answer_variant, битый regex в `when`),
  `TestValidateRejectsCallerProfileFieldsInPreparedDialogue` (4
  подслучая: `caller` и три новых поля факта на prepared-диалоге).
  `TestValidateFreeTextFullCase` (112-5a форма без профиля) осталась
  зелёной без изменений — подтверждает обратную совместимость.

Validation: `gofmt -l` (чисто), `go build ./...`, `make verify` (полностью
зелёный — format-check/build/test/vet/staticcheck), `python3
design-docs/contracts/check.py` (все проверки прошли, включая
`scenario.example.json` против обновлённой схемы). `make verify-web` не
запускался — TS-код в этом коммите не менялся, только перегенерированный
игнорируемый файл.

Next: коммит c3 — расширить результат хода (`CallerReply.Source/
Reveals/Generation`, `IntakeCallerTurn.Source/Generation`,
`ApplyCallerReply`) в `internal/training`/`operator112`, метка адаптера
переезжает из `CallerReplier.Adapter()` в сам `CallerReply`.

## 2026-09-25 — Срез 112-5b, коммит 3: source/reveals/generation в результате хода

Расширяет результат `caller.reply`, не подключая ещё ни модель, ни
классификатор — `StubCallerReplier` остаётся единственной реализацией
`CallerReplier`, но теперь сама называет свой `adapter`/`source`, а не
через отдельный метод интерфейса:

- `internal/training/operator112/caller.go`: `CallerReplyRequest`
  получил `Caller *content.Intake112CallerProfile`. `CallerReply` —
  `{Text, Adapter, Source, Reveals, Generation
  *training.IntakeCallerGeneration}`. `CallerReplier.Adapter()` убран из
  интерфейса — адаптер и источник теперь называет сам ответ, поэтому
  один будущий `aicaller.Replier` сможет в одном и том же `Reply` для
  одного хода вернуть `source=model`, а для другого (сценарий без
  профиля заявителя) — `source=stub`.
- `internal/training/intake.go`: `IntakeCallerTurn.Source` и
  `.Generation *IntakeCallerGeneration` (`omitempty`); константы
  `CallerTurnSourceOpening/Scripted/Model/Fallback/Stub`;
  `IntakeCallerGeneration{Model, PromptVersion, Temperature, TopP,
  RepeatPenalty, MaxTokens}` — заполняется только для `model`/`fallback`
  (RFC-001 §6 «производные входы оценки тоже фиксируются» — то же
  правило распространено на реплики заявителя).
- `internal/training/service.go`: новый `CallerReplyOutcome{Text,
  Adapter, Source, Reveals, Generation}`; `ApplyCallerReply` принимает
  его вместо пары `text, adapter string` — пишет `Source`/`Generation`
  в ход и `Reveals` в новую строку `transcript` (поле `reveals` там уже
  существовало для сценарного 112-2/112-3, теперь используется и здесь).
- `internal/training/operator112/stub_caller.go`: `Reply` сам
  возвращает `Adapter: StubCallerReplierAdapter, Source:
  training.CallerTurnSourceStub` вместе с текстом; метод `Adapter()`
  удалён.
- `cmd/emsim/worker_composition.go` (`callerReplyHandler`): передаёт
  `Caller: replyCtx.Dialogue.Caller` в запрос, строит
  `training.CallerReplyOutcome` из ответа `CallerReplier` вместо
  `reply.Text, replier.Adapter()`.
- Контракты: `evidence.operator112.schema.json` и `openapi.yaml`
  (`Intake112State`) — `caller_turns[].source` (enum) и `.generation`
  (`model`, `prompt_version` обязательны; `temperature`/`top_p`/
  `repeat_penalty`/`max_tokens` опциональны); старые evidence и API-ответы
  без этих полей остаются валидны (оба необязательны).
- Тесты: `internal/training/operator112/stub_caller_test.go` —
  `TestStubCallerReplierSequenceAndRepeat` теперь проверяет
  `Adapter`/`Source` на каждом ходе. `test/integration/
  operator112_caller_chat_test.go` — обновлён вызов `ApplyCallerReply`
  под новую сигнатуру (`CallerReplyOutcome`); ассерт на ответ реального
  worker дополнен проверкой `Source == training.CallerTurnSourceStub`.

Validation: `gofmt -l` (чисто), `go build ./...`, `go vet ./...` и
`go vet -tags integration ./...` (оба чистые), `make verify` (зелёный),
`python3 design-docs/contracts/check.py` (все проверки прошли), `make
test-integration` — зелёный, включая `TestCallerChat*` набор
(153.975s, Docker Postgres). `make verify-web` не запускался — TS-код
не менялся (только `Intake112Dialogue.caller` из коммита 2, уже
перегенерирован).

Next: коммит c4 — `internal/platform/llm/` (минимальный клиент
OpenAI-совместимого `/v1/chat/completions`) и
`internal/training/operator112/aicaller/` (state/classify/prompt/
replier), fallback на нейтральную реплику при исчерпании попыток в
`callerReplyHandler`, новые поля `config.Worker`
(`CallerReplier`/`CallerLLMURL`/`CallerLLMModel`/генерационные
параметры), `compose.yaml`.

## 2026-09-25 — Срез 112-5b, коммит 4: клиент модели и ИИ-адаптер

Подключает модель за портом `CallerReplier` без изменения протокола
ADR-024; по умолчанию (`CALLER_REPLIER` не задан) поведение равно
112-5a — `StubCallerReplier` остаётся единственным исполнением:

- `internal/platform/llm/` (новый пакет): `Client.Complete` — один
  `POST {base}/chat/completions`, `net/http`, без новых зависимостей;
  `Request{Model, Messages, Stream=false, Temperature, TopP, MaxTokens,
  RepeatPenalty}` (`repeat_penalty` — расширение llama.cpp, Ollama может
  игнорировать — отмечено в доккомменте и ADR-025); ошибки не содержат
  тела ответа (RFC-001 §9). Переиспользуется 112-6 для
  `assessment.evaluate`, когда он появится.
- `internal/training/operator112/aicaller/` (новый пакет):
  `state.go` — `OpenFacts`/`RevealedFacts` восстанавливают состояние из
  транскрипта заново на каждый ход (без сохранения между вызовами,
  решение 1 плана); `classify.go` — `FactAsked` (`ask_patterns` минус
  `ask_exclude_patterns`, спецслучай «адрес vs ориентировка» теперь в
  данных сценария), `DiscloseReveals` (регэкспы `disclosure_patterns`,
  телефон — по последним 10 цифрам, как в `caller.py`), общий кеш
  скомпилированных `(?i)`-паттернов; `prompt.go` — раскладка под кеш
  префикса llama.cpp (решение 4): `system` = статичные правила
  (дословно из `caller.py`) + персонаж — неизменны в пределах диалога;
  история — как есть; факты/задача хода — в последнем `user`-сообщении;
  `replier.go` — `Replier.Reply`: нет профиля → делегирует `Stub`; ход 1 →
  `Caller.Opening` без модели (`Reveals` — из `Opening.Reveals`
  сценария, тот же принцип, что у `dialogue.initial.reveals` 112-2);
  ровно один спрошенный факт с подходящим `answer_variant` →
  без модели; иначе — вызов `ChatCompleter` (порт, объявленный здесь;
  реализация — `platform/llm`), `CleanReply` вырезает `<think>…</think>`
  и обрезает до последней границы предложения при
  `finish_reason=length`, пустой ответ — ошибка; `Fallback(req)` —
  нейтральная реплика с `source=fallback` и записанными параметрами
  генерации.
- `cmd/emsim/worker_composition.go`: `callerReplyHandler` получил
  `maxAttempts int, fallback func(...) operator112.CallerReply`;
  `callerReplyFallbackOutcome` (чистая функция) решает — fallback
  применяется, только если `fallback != nil` (т.е. `CALLER_REPLIER=llm`)
  и это последняя попытка (`lease.Attempt >= maxAttempts`, то же
  условие, что `ResolveFailureTx`, `internal/platform/tasks/
  pgrecovery.go`, использует для остановки retry) — иначе обычный
  retryable `HandlerFailure`, как в 112-5a. `composePools` выбирает
  `operator112.StubCallerReplier` или `aicaller.Replier` по
  `config.Worker.CallerReplier`, `MaxAttempts` берёт из
  `registry.Lookup(training.KindCallerReply)`, а не дублирует константу.
- `internal/platform/config/worker.go`: `CallerReplier` (`stub`|`llm`,
  по умолчанию `stub` при пустом `CALLER_REPLIER` — свойство сохранения
  совместимости, стек без модели работает как раньше),
  `CallerLLMURL`/`CallerLLMModel` (обязательны при `llm`),
  `CallerTemperature`/`CallerTopP`/`CallerRepeatPenalty`/`CallerMaxTokens`
  с дефолтами MVP (0.3/0.9/1.1/150, `handoff/README.md`); `Validate`
  проверяет допустимые диапазоны и обязательность URL/модели при `llm`.
- `compose.yaml`: `CALLER_REPLIER`, `CALLER_LLM_URL` (по умолчанию
  `http://host.docker.internal:11434/v1`), `CALLER_LLM_MODEL`,
  `extra_hosts: host.docker.internal:host-gateway` у `worker`; заголовок
  файла обновлён (llm-клиент существует, `llama-server`-контейнер — ещё
  нет, это этап 2).
- Тесты: `internal/platform/llm/client_test.go` — форма запроса
  (`httptest`), `stream=false`, код ответа ≠200 (без утечки тела в
  ошибку), отмена по `ctx`, пустой `choices`.
  `internal/training/operator112/aicaller/replier_test.go` — вступление
  без вызова модели (`erroringChat`), делегирование заглушке без
  профиля, исключение «ориентировка ≠ адрес», фиксированный ответ (и его
  пропуск при >1 спрошенном факте), очистка `<think>`/генерация в
  результате, распространение ошибки модели, отказ на пустом ответе,
  **системное сообщение побайтно одинаково между 2 и 3 ходом** (решение
  4 проверено явно), закрытые факты не попадают в промпт, fallback,
  `CleanReply` (обрезка по `finish_reason=length` и без нее).
  `internal/platform/config/worker_test.go` — дефолт `stub` без единой
  переменной `CALLER_*`, `llm` без URL/модели отклоняется, `top_p` вне
  `[0,1]` отклоняется, неизвестное значение `CALLER_REPLIER` отклоняется.
  `cmd/emsim/worker_test.go` — `callerReplyFallbackOutcome`: не
  последняя попытка → retry без вызова fallback; последняя попытка с
  fallback → применяется; `fallback=nil` (заглушка) → никогда не
  применяется, даже на последней попытке.

Validation: `gofmt -l` (чисто), `go build ./...`, `go vet ./...` и
`go vet -tags integration ./...` (чистые), `make verify` (зелёный, все
новые пакеты включены), `python3 design-docs/contracts/check.py`
(зелёный), `docker compose config` (валиден), `make test-integration`
— зелёный (155.569s, прежний набор 112-5a/ДДС на заглушке без
изменений — `CALLER_REPLIER` не установлен нигде в интеграционных
тестах, поэтому они по-прежнему используют `StubCallerReplier`).
Ручная проверка с реальным Ollama не выполнялась в рамках этого
коммита — сценариев с `dialogue.caller` пока нет (появятся в коммите
c5); `CALLER_REPLIER=llm` сейчас отвечал бы заглушкой для всех
существующих сценариев (нет профиля заявителя), что и есть
задокументированное поведение решения 7.

Next: коммит c5 — три новых сида в `seed/scenarios/` с профилем
заявителя, перенесённые из YAML пользователя
(`claude_caller_mvp_20260925.zip`), с заглушкой-эталоном там, где
каталог типов происшествий не покрывает сценарий; `seed/README.md`.

## 2026-09-25 — Срез 112-5b, коммит 5: сценарии для ручной проверки ИИ-заявителя

Три новых `full_case`/`caller_mode=free_text` сценария в `seed/scenarios/`
с заданным `dialogue.caller` (профиль появился в коммите 2, но
`pilot-112-free-text-chat-01` его не использует — эти три первые
сценарии в проекте, где под `CALLER_REPLIER=llm` отвечает
`aicaller.Replier`, а не заглушка). Персонажи, факты, фразы и
регэкспы перенесены из локального MVP пользователя
(`claude_caller_mvp_20260925.zip`, `handoff/README.md` 2026-09-25) —
`toyota_fire_driver_burns_01`/`car_in_water_01`/
`argument_mobile_shop_01.yaml`; `leak_keywords` (тестовое поле MVP) не
переносится:

- `pilot-112-ai-toyota-fire-01.json` — свидетель возгорания автомобиля
  с ожогами у водителя; 12 фактов (5 `initial`, 7 `on_question`, узлы
  без `card_path` для номера машины и имени пострадавшего водителя —
  нарративные факты без поля карточки, решение c2). `expected_types:
  ["road_traffic_fire"]`, `expected_services: ["pilot_fire_101"]` — не
  заглушка, реальное соответствие ситуации.
- `pilot-112-ai-car-in-water-01.json` — падение автомобиля в воду;
  факт `ambulance` с `answer_variants: [{text: "..."}]` без `when`
  (безусловный вариант, ответ «не знаю, есть ли люди в машине» без
  вызова модели — проверка решения 3 плана). `address`-факт получил
  `ask_exclude_patterns: ["ориентировк","местоположени"]`, отдельный
  `landmark`-факт — их и проверяет решение 5 плана (адрес не
  перехватывает вопрос про ориентир). `expected_types`/
  `expected_services` — заглушка (тип «падение в воду» не входит в
  каталог), `case_description` явно это называет.
- `pilot-112-ai-mobile-shop-01.json` — ссора с продавцом салона связи;
  намеренно без отдельного факта-ориентира (в отличие от двух других) —
  прямая проверка риска выдумывания места, зафиксированного в
  `slice-112-5b-plan.md`'s «Риски». `expected_types`/`expected_services`
  — тоже заглушка, `applicant_status="participant"` (заявитель сам
  участник конфликта, а не свидетель/пострадавший).
- `pilot-112-free-text-chat-01.json` не менялся — 112-5a e2e и `stub/v1`
  без `dialogue.caller` остаются прежними.
- `seed/README.md` — описание всех трёх файлов, явное указание, у каких
  двух эталон-заглушка и почему.

Validation: `go test ./internal/content/... -run TestSeedFilesAreValid`
(схемная и семантическая валидация всех файлов `seed/scenarios/`,
включая три новых — зелёный), `make verify` (зелёный), `python3
design-docs/contracts/check.py` (зелёный). Ручной прогон:
`docker compose up --build` (после `down -v` для чистого тома) —
контейнер `seed` завершился с кодом 0, лог `scenarios: new_scenarios=15
new_versions=16 unchanged=0`; прямой запрос к БД подтвердил все три
новых `source_key` со статусом `approved`. Стек остановлен и очищен
(`docker compose down -v`) после проверки. С реальной моделью
(`CALLER_REPLIER=llm` + Ollama) кейсы не прогонялись в рамках этого
коммита — это ручная проверка пользователя на платформе (вне объёма
автоматических коммитов c1–c6, см. `slice-112-5b-plan.md`).

Next: коммит c6 — UI разбора преподавателя (`ItemReview.tsx`: источник
хода и модель/версия промпта из `generation`), `README.md` (запуск с
Ollama на Mac), `slice-planning-112.md`/`CLAUDE.md`/`LOG.MD` — этап 1
среза 112-5b объявляется реализованным (этап 2 — W0/`llama-server` —
остаётся планом).

## 2026-09-25 — Срез 112-5b, коммит 6: UI разбора, документация, этап 1 завершён

Финальный коммит этапа 1 (`slice-112-5b-plan.md`): UI преподавателя и
документационный проход:

- `web/src/routes/instructor/ItemReview.tsx`: `IntakeReviewCallerTurn`
  получил `source`/`generation` (типы из контракта c3); `turnStatusText`
  для отвеченного хода показывает источник по-русски (вступление/
  фиксированный ответ/модель/нейтральная реплика — модель недоступна/
  заглушка) и, если есть `generation`, модель и версию промпта через
  `·`; при отсутствии `source`/`generation` (evidence, запечатанное до
  112-5b) откатывается к прежнему отображению одного `adapter`. UI
  обучаемого (`CallerChat.tsx`) не менялся — нейтральная реплика при
  отказе модели выглядит как обычная реплика заявителя, как и
  предусматривало решение 6 плана.
- `web/e2e/operator112.spec.ts`: ассерт разбора обновлён под новый
  текст (`/Отвечено \(заглушка\)/` вместо `/Отвечено \(stub\/v1\)/` —
  заглушка теперь даёт `source=stub`, который и отображается); добавлен
  прямой ассерт `turn.source === "stub"` на все отвеченные ходы кейса
  (сама заглушка никогда не помечает `source` как `model`/`fallback`).
- `README.md`: новый абзац после описания 112-5a — этап 1 112-5b
  (ADR-025), `CALLER_REPLIER` по умолчанию `stub` (без изменений
  поведения), `CALLER_REPLIER=llm` + инструкция запуска Ollama на Mac
  (`ollama serve`/`ollama pull`/`docker compose up`), переменные
  `CALLER_LLM_URL`/`CALLER_LLM_MODEL` и их дефолты, три новых сида,
  что видно в «Разборе», поведение при отказе модели, явная отсылка к
  этапу 2 (W0/`llama-server`) как остающейся отдельной работе. Список
  планов среза 112 дополнен `slice-112-5b-plan.md`.
- `slice-planning-112.md`: шапка статуса, строка 112-5b в таблице §3,
  §9 «Статус» и заключительный «Следующий шаг» — везде явно разделены
  «этап 1 реализован» / «этап 2 — план»; срез 112-5b как целое не
  объявлен завершённым.
- `CLAUDE.md`: «Current delivery state» — 112-5b этап 1 добавлен в
  список реализованного с описанием (`aicaller`, `CALLER_REPLIER`,
  дефолт `stub`), «remaining order» теперь начинается с этапа 2 112-5b,
  а не с всего среза; карта кода про `ItemReview.tsx` дополнена
  source/generation; ADR-003-инвариант переписан явно как «ADR-003 для
  всего, кроме caller.reply 112; для него — узкое исключение ADR-025».

Validation: `gofmt -l` (чисто), `make verify` (зелёный), `python3
design-docs/contracts/check.py` (зелёный), `make verify-web`
(`tsc -b`/`vite build` зелёные), `cd web && npm run lint` (0 ошибок,
один прежний warning в несвязанном файле), `cd web && npm run test:e2e`
— все 6 сценариев зелёные (включая обновлённый разбор чата и
`operator112-profiles`/`smoke`/полный кейс 112-4), контейнеры подняты и
остановлены самим раннером (`docker compose -p emsim-e2e-... down
--volumes`). `make test-integration` перепрогонялся в коммите 4 после
последних изменений `internal/training`/`cmd/emsim`, в этом коммите
código не менялся — не перезапускался. Отдельно вручную:
`docker compose up --build` после `down -v` — контейнер `seed` завершился
кодом 0, три новых сценария подтверждены прямым запросом к БД
(коммит 5); стек остановлен и очищен.

Итог этапа 1 среза 112-5b: `CallerReplier` подключает модель через
`internal/platform/llm`+`internal/training/operator112/aicaller`
(ADR-025), не меняя протокол ADR-024; по умолчанию (`CALLER_REPLIER`
не задан) поведение — 112-5a без изменений. Коммиты c1–c6 реализованы
и покрыты тестами (`go test`/contracts check/e2e); ручная проверка
с реальным Ollama (`CALLER_REPLIER=llm`) на трёх новых сценариях —
задача пользователя на платформе, вне этих коммитов.

Next: этап 2 среза 112-5b — замер W0 на целевом сервере (6 CPU, без
GPU): задержка и качество ответа `caller.reply` на русском при
concurrency 1/2/4 через реальный локальный инференс; по результатам —
выбор модели/квантизации, `CALLER_CONCURRENCY`/`CALLER_REPLY_TIMEOUT`
для продакшна, контейнер `llama-server` с GGUF в `compose.yaml`,
решение по ёмкости класса (`slice-planning-112.md` §3.1). До этого
срез 112-5b не закрыт; 112-6 (оценка 112) — следующий срез по плану
после закрытия 112-5b.

## 2026-09-25 — 112-6: план детерминированной оценки (решение пользователя)

Decision: по решению пользователя 112-6 (оценка 112) начинается до этапа 2
112-5b (замер W0 пользователь сделает позже); 112-5b остаётся незакрытым.
Согласована 100-балльная рубрика: адрес 35 (по полям), профильные карты 25
(поровну между ожидаемыми картами, доля верных ответов), разговор 15
(темы по словам в репликах оператора), тайминг 7,5+7,5 (ответ 15 с,
заполнение 90 с), описание 10 (непустое), штрафы: страна/субъект −10, ФИО
−5, служба −5 за каждую, лишняя карта −5. Сравнение — с эталоном сценария
(расширяется `intake112.reference`), не с транскриптом. LLM-критериев на
этом этапе нет.

Changed: `slice-112-6-plan.md` (новый), `slice-planning-112.md` (статус
112-6, «Следующий шаг»). Код не менялся, проверки не запускались.

Findings for implementation: закрытие 112 сейчас не ставит
`assessment.evaluate` (`training.Service.enqueueEvaluateWaiting`);
`operator112/rubric-v1` зашита при создании занятия; `sealInputForItem`
читает evidence как ДДС и грузит `LoadDefault()` (рубрику ДДС); в
`assessment.Registry` только ДДС; эталон адреса в сидах 112 почти пуст
(только `descriptive` в ИИ-кейсах).

Open: интерпретации §10 плана (−10 один раз, штраф и за пропущенную службу,
пустое ФИО только при сообщённом, линейная шкала тайминга N…2N) ждут
подтверждения пользователя.

Next: коммит 1 плана — ADR-026 и контракты (rubric/scenario/openapi).

## 2026-09-25 — 112-6, коммит 1: ADR-026 и контракты рубрики v2

Все шесть интерпретаций §10 предыдущего плана подтверждены пользователем
("Да, все шесть"). Новое явное решение: эталон в сидах пока не
наполняется, но инфраструктура (схема + валидация импорта, конвейер
оценки) уже должна поддерживать полный эталон на кейс — добавление
эталона позже станет новой версией файла сценария, не изменением кода.
Отсутствие эталона у блока/штрафа — обычный `0` по той же бизнес-логике
("просто 0, бизнес логика как мы описали"), не отдельный статус
`not_applicable`/`needs_review`/`unavailable`.

Реализован коммит 1 согласованного покоммитного плана (сессионный план
Plan Mode + `slice-112-6-plan.md`, пересозданный в этом коммите — файл,
упомянутый в предыдущей записи как "новый", на диске отсутствовал на
момент начала этого коммита; пересоздан по содержанию текущего решения
и ADR-026, без утраченной точной таблицы баллов по 15 полям исходного
черновика — итоговое распределение 35 баллов по 12 полям адреса
(страна/субъект/округ/район/город/улица/дом/корпус/строение/квартира/
подъезд/этаж, без объекта/ориентира) задано в `rubric.operator112.json`'s
`params` и открыто для правки одним файлом):

- `design-docs/adr/026-operator112-assessment-rubric-v2.md` (новый) —
  полное решение: рубрика v2 (6 блоков + 4 штрафа), сравнение с
  эталоном а не транскриптом, таблица «эталон отсутствует → 0», что
  именно проверяется (`notification.card_snapshot` → `final_card`
  fallback), legacy-маршрут → `operator112_legacy_route`, рубрика по
  версии, LLM — только v3.
- `design-docs/contracts/rubric.schema.json` — `kind` получает `penalty`
  (обязателен `rule`, запрещены `prompt`/`sources`, как у `deterministic`).
- `design-docs/contracts/rubric.operator112.json` → `operator112/rubric-v2`
  (6 критериев-блоков + 4 `kind:penalty`); старая ручная рубрика
  сохранена как `rubric.operator112.v1.json` (embed.go).
- `design-docs/contracts/scenario.schema.json` — `intake112.reference`:
  `expected_card` разрешён для `card_only`/`full_case` (не только
  `incoming_call`), поля `expected_card.address` расширены
  (country/region/okrug/district/structure/flat/entrance/floor),
  `applicant_status`/`incident_type` из `const` в свободную строку
  (готовые сиды используют более одного значения — `victim` только для
  incoming_call пилота, 112-3/4 используют `witness`/`participant`/др.),
  `applicant_name`, `alternatives`, `expected_profiles`,
  `expected_services` разрешён и для `card_only`; общий `$defs/scoring`
  вынесен из ДДС-ветки и переиспользован в `intake112.reference`.
- `design-docs/contracts/openapi.yaml` — `CriterionResult.penalty_points`/
  `.details[]` (новая `CriterionDetail`).
- `design-docs/contracts/check.py` — рубрика v1/v2 валидны и различимы,
  штраф без `rule` отклоняется, синтетический сценарий с полным эталоном
  (адрес+ФИО+профили+альтернативы+scoring) валиден без правки `seed/`.

Validation: `python3 design-docs/contracts/check.py` (зелёный, все
существующие и новые проверки), `go build ./...` (чисто).

Next: коммит 2 — `internal/content`: расширение
`Intake112Reference`/`Intake112ExpectedCard`, валидация импорта против
каталога/рубрики, общая нормализация значений (`internal/content/normalize`).

## 2026-09-25 — 112-6, коммиты 2–6: детерминированный конвейер оценки 112 реализован целиком

Продолжение коммита 1 (ADR-026, контракты) без перерыва в этой же
сессии. Коммиты 2–6 согласованного покоммитного плана
(`slice-112-6-plan.md`, план Plan Mode) реализованы и закоммичены по
отдельности; ниже — сводка, детали в собственных commit message
каждого коммита.

- **c2** (`internal/content`): `Intake112Reference`/`Intake112ExpectedCard`
  расширены (`Alternatives`, `ExpectedProfiles`, `Scoring`,
  `ApplicantName`, полный адрес без object/landmark) — все поля
  необязательны, старые сиды продолжают валидироваться без изменений.
  Валидация импорта проверяет `expected_profiles` против каталога,
  `alternatives` против известных путей, `scoring` против критериев
  `operator112/rubric-v2`, и что `expected_card` не противоречит факту
  диалога с тем же `card_path`. Новый пакет `internal/content/normalize`
  (регистр/ё/пунктуация/сокращения/порядок слов) — общий для валидации
  и будущего оценщика.
- **c3** (`internal/assessment`): `RuleEvaluator.Evaluate` теперь берёт
  сырое evidence (`json.RawMessage`) и весь `content.Body` вместо
  типизированного `training.EvidenceBody` — `assessment/dds` декодирует
  свою типизированную форму сама, поведение не изменилось.
  `LoadRubric(exerciseType, version)` грузит рубрику именно по версии
  занятия (не «текущую»); `sealInputForItem` перестал жёстко звать
  DDS'ный `LoadDefault()` для 112 — это была прямая причина, по которой
  112 не мог получить автооценку вообще. `CreateExpertRevision`/`Get`
  переиспользуют `RubricEffective` уже существующей финальной оценки,
  когда она есть, и лезут за версией занятия (новый порт `LessonReader`)
  только для самой первой оценки по item. `Score()` — новый `kind:
  penalty`, штрафы исключены из нормировки и вычитаются после нее,
  клампится в [0,100]. `CriterionResult` получил `PenaltyPoints`/
  `Details` (jsonb, без миграции). Добавлен `*TerminalEvaluationError`
  — способ оценщика назвать конкретный код отказа задачи, не создавая
  цикл импорта между `assessment` и `assessment/operator112`.
- **c4** (`internal/training`): `content.RubricVersionFor(exerciseType)`
  — новое занятие 112 теперь фиксирует `operator112/rubric-v2` (было
  жёстко `v1` всегда). `enqueueEvaluateWaiting` ставит
  `assessment.evaluate` для любого 112-занятия training-режима, кроме
  оставшихся на `rubric-v1`. Один существующий интеграционный тест
  (`TestOperator112IntakeTransaction`) обновлён под новое поведение —
  рубрика v2 при создании занятия, `assessment.evaluate` теперь ставится
  и для legacy incoming_call/dispatch_intake путей (отличить legacy от
  обычного можно только на этапе seal, не enqueue). Также найдены и
  исправлены две интеграционные проверки с устаревшим числом сценариев
  в сидах (12/13 вместо актуальных 15/16 — не учли три сценария 112-5b,
  видимо пропущено в той более ранней сессии) и два места, не
  учитывавшие новый параметр конструктора `assessment.NewService` под
  тегом `integration` (`go vet`/`go test` без тега их не видят).
- **c5** (новый пакет `internal/assessment/operator112`): `ADDRESS_FIELDS`,
  `PROFILE_CARDS`, все четыре штрафа. Оцениваемый снимок —
  `notification.card_snapshot`, иначе `final_card`. Legacy-маршрут
  (`intake_state.finale == ""` — это в том числе КАЖДЫЙ `incoming_call`
  item, не только явно старые) прерывает всю оценку сентинел-ошибкой →
  `failed/operator112_legacy_route`.
- **c6** (тот же пакет): `CALLER_TOPICS` (словарь RE2, реплики
  оператора, недостижимые по сценарию темы — `not_applicable` с
  перенормировкой блока), `T_ANSWER`/`T_FILL` (линейная шкала N..2N,
  вычитание ожидания ответа ИИ-заявителя из `T_FILL`), `DESCRIPTION_
  PRESENT`, карве-аут ИИ-расхождения для `free_text` (несовпадающее с
  эталоном поле, которое встречается в реплике заявителя с
  `source=model|fallback` → `unavailable` у всего критерия →
  `needs_review`). Регистрация в `cmd/emsim/assessment_composition.go`
  — конвейер теперь реально работает для 112, не только для ДДС.

Validation (сквозная для 2–6): `gofmt -l`, `go vet ./...`, `go vet
-tags=integration ./test/integration/...`, `go test ./...` (все
пакеты, включая новые `internal/content/normalize` и
`internal/assessment/operator112`), `python3 design-docs/contracts/
check.py`, и дважды полный `go test -tags=integration ./test/
integration/...` с реальным Postgres (testcontainers) — после c4 и
повторно после c6 (с реально зарегистрированным оценщиком). Все
зелёные.

Open / известные упрощения, зафиксированные в коде комментариями:
- Баллы по полям адреса (35 на 12 полей) и словарь тем разговора в
  `rubric.operator112.json` — первая рабочая конфигурация, не точное
  воспроизведение исходного черновика пользователя (тот файл
  `slice-112-6-plan.md` с точной таблицей был утрачен до начала
  реализации — см. коммит 1). Меняются одним файлом контракта, без
  кода.
- ИИ-расхождение (`aiDivergent`) — консервативная глобальная эвристика
  (есть ли в принципе реплика заявителя с нужным значением и была ли
  вообще хоть одна ИИ-реплика в разговоре), не точное сопоставление
  поля конкретному ходу диалога — транскрипт не хранит номер хода на
  строке. Задокументировано в коде; при ложноотрицательном срабатывании
  результат — обычный `not_met`, который ручная проверка всё равно
  может исправить.

Next: коммит 7 — интеграционные тесты полного конвейера 112 (закрытие
→ waiting → sealed input → auto rev=1 ready; retry/lease loss; stop до
оповещения; legacy → failed/operator112_legacy_route + ручная ревизия;
expert после auto; занятие v1 не ставит задачу). `make test-integration`.

## 2026-09-25 — 112-6, коммит 7: интеграционные тесты полного конвейера 112

Новый `test/integration/operator112_assessment_pipeline_test.go` —
шесть тестов, зеркало `assessment_pipeline_test.go` для реального
`operator112_intake` full_case-кейса (`pilot-112-full-gas-road-traffic-
fire-01`), теперь когда оценщик реально зарегистрирован (коммит 6):
полный цикл close→waiting→coordinator→pending→Handle→auto rev=1
ready (не needs_review — в этой рубрике нет llm-критериев, в отличие
от ДДС); повтор задачи не создаёт вторую auto; stop до
`notify_services` — auto всё равно `ready`, `T_FILL`/`P_SERVICES`
`not_applicable`; легаси-маршрут (`incoming_call`/`dispatch_intake` под
свежим `rubric-v2` занятием) — `failed/operator112_legacy_route`, auto
не создаётся, ручная ревизия работает; expert после auto становится
итоговым, auto-строка остаётся нетронутой; занятие на `rubric-v1`
(искусственно — `CreateLesson` больше не может его создать после
коммита 4) не ставит задачу вообще.

Пришлось дважды поправить сборку сценариев: `pilot-112-medical-01` в
реальном сиде имеет более новую v2 (диалоговую) версию, которая
подменяет v1 как `approved` при импорте всей директории — тест сначала
брал `version=1` напрямую и получал `not approved`; переключился на
выбор по `status='approved'`. Для легаси-теста в итоге всё равно
понадобился отдельный (не из сида) `incoming_call`-сценарий —
скопирован приём `TestOperator112IntakeTransaction`'а: тело
`pilot-112-medical-01.json` вставляется напрямую под собственным
`source_key`, в обход `ImportScenarios`, чтобы не зависеть от того,
какая версия сейчас approved в сиде.

Validation: `gofmt -l`, `go vet ./...`, `go vet -tags=integration
./test/integration/...`, `go test ./...`, `python3 design-docs/
contracts/check.py`, полный `go test -tags=integration ./test/
integration/...` (реальный Postgres, testcontainers) — все зелёные.

Next: коммит 8 — UI разбора преподавателя (`ItemReview.tsx`: автооценка
для 112 с таблицей блоков и раскрываемыми `details`, штрафы отдельно,
числовые поля в форме ревизии) и результатов обучаемого
(`History.tsx`: итог/блоки/штрафы без `expected`), типы из
`openapi.yaml`, e2e `operator112.spec.ts`.

## 2026-09-26 — 112-6, коммит 9: отчётность — блоки и штрафы 112 в отчёте, CSV и PDF

`internal/reporting/domain.go`: `ReportItem.IntakeBlocks
[]IntakeBlockScore` и `.IntakePenaltyTotal *float64`; новый тип
`IntakeBlockScore{CriterionID,Label,Points,MaxPoints}`. `internal/
reporting/postgres/store.go`: `criterionLabels` расширен десятью id
рубрики v2, новые `intake112BlockIDs`/`intake112PenaltyIDs`,
`intake112Breakdown`/`intakeBreakdownFor` читают уже
запечатанные `score`/`weight`/`penalty_points` из `criteria` jsonb
итоговой ревизии (гейт на `AssessmentReady`, как `publicErrors`) —
для ДДС-строк оба поля остаются nil. `csv.go`: два новых хвостовых
столбца «Блоки 112»/«Штрафы 112» (пусты для ДДС), новый хелпер
`intakeBlocksText`. `pdf.go`: те же две строки в параграфах карточки,
если поля заданы. `openapi.yaml`: `ReportItem.intake_blocks`/
`.intake_penalty_total`, новая схема `IntakeBlockScore`.
`web/src/api/reporting.ts` + `LessonReport.tsx`: те же два столбца в
таблице «Карточки» только для `operator112_intake` занятий; убрана
старая заглушка «CSV и PDF для 112 появятся в срезе 112-5» — экспорт
теперь общий для обоих типов упражнения.

Validation: `go build ./...`, `gofmt -l` (весь `internal/reporting`),
`go vet ./...`, `go test ./...`, `python3 design-docs/contracts/
check.py`, `cd web && npx tsc -b`, `npm run build`, `npm run lint`
(тот же единственный пред-существующий warning в
`Operator112ProfileCase.tsx`), `make verify`, `make verify-web` — все
зелёные. Не выполнялись: `make test-integration`, `npm run test:e2e`
(отчётность не меняет БД/воркер, только чтение уже существующих
данных — интеграционные тесты коммита 7 остаются актуальной защитой).

Next: коммит 8 (UI `ItemReview.tsx`/`operator112.spec.ts`, уже
написан, но ещё не закоммичен и не проверен вручную) — открыть живой
docker-compose стек в браузере, визуально проверить новую таблицу
автооценки, прогнать `npm run test:e2e` и закоммитить; затем коммит 10
(документация: README, seed/README, slice-planning-112.md, CLAUDE.md).

## 2026-09-26 — 112-6, коммит 8 и правка коммита 9: UI разбора + снятие старой блокировки экспорта

Закоммитил уже написанный (с прошлой сессии) `ItemReview.tsx`: для
`operator112_intake` — таблицы «Блоки»/«Штрафы» (`IntakeCriteriaTable`,
раскрываемые `details`) вместо статус-таблицы ДДС, форма ревизии —
числовые баллы по блоку/штрафу (`buildIntakeRevisionCriteria`
переводит их обратно в `CriterionResult[]`: доля 0..1 от веса для
блока, `penalty_points` напрямую для штрафа). `assessment.ts`:
`rubric_effective.criteria` получил `title`/`kind`. `operator112.spec.ts`:
новые проверки автооценки в full_case-тесте (адрес 0/35 — эталона
ещё нет; штраф по службам −0) и переписанная ручная ревизия в первом
тесте (три статус-селекта → числовые баллы, итог 82.5).

Пересобрал compose-стек (`docker compose up --build -d`) и прогнал
`npm run test:e2e` вживую (изолированный e2e-проект из `web/e2e/
run.mjs`) — сначала упал `operator112.spec.ts`'s full_case-тест:
`page.locator("tr", {hasText: "Адрес происшествия"})` стал матчить
две строки (новую таблицу «Блоки» и форму ревизии — обе показывают
title критерия). Исправил, ограничив локатор `h3:has-text("Блоки") +
table`/`h3:has-text("Штрафы") + table`. После этого все 6 e2e-тестов
зелёные.

Ручная проверка в браузере (обязательство CLAUDE.md по UI): создал
занятие 112 через UI, прошёл `card_only`-кейс (взрыв газа + ДТП)
трейни, закрыл карточку — обнаружил, что `internal/reporting/http/
handlers.go`'s `exportAvailable` до сих пор блокирует `report.csv`/
`report.pdf` для `operator112_intake` 409-ом «available in slice
112-5» — остаток до коммита 6, о котором коммит 9 забыл (фронт уже
показывал колонки, но сам экспорт всё равно отказывал). Убрал
`exportAvailable` и оба вызова целиком; проверил вручную (`curl` с
сессией инструктора) — `report.csv` отдаёт строку с блоками/штрафами,
`report.pdf` формируется и скачивается как валидный PDF.

Изменённые файлы: `web/src/routes/instructor/ItemReview.tsx`,
`web/src/api/assessment.ts`, `web/e2e/operator112.spec.ts` (коммит
`817c8c0`); `internal/reporting/http/handlers.go` (коммит `d14d342`).

Validation: `go build`/`vet`/`test ./...`, `gofmt -l`, `python3
design-docs/contracts/check.py`, `cd web && npx tsc -b && npm run
build && npm run lint` (тот же единственный пред-существующий
warning), `make verify`, `make verify-web`, полный `npm run test:e2e`
(6/6 зелёных) дважды — до и после правки handlers.go — плюс ручная
сквозная проверка в браузере через реальный занятие/карточку/разбор/
отчёт.

Next: коммит 10 — документация (`README.md`, `seed/README.md`,
`slice-planning-112.md`, `CLAUDE.md`), финальная запись в LOG.MD,
проверка, что примечание о будущем прямом редактировании поля
«Адрес» в конце `slice-112-6-plan.md` не потеряно.

## 2026-09-26 — 112-6, коммит 10: документация и завершение среза

Последний коммит плана 112-6 (`slice-112-6-plan.md`, c1–c10, все
предыдущие коммиты — `112-6 c1`…`c9` плюс отдельная правка c9 —
уже в истории). Обновил:

- `README.md`: новый раздел «Автоматическая оценка оператора 112
  (срез 112-6)» — рубрика `operator112/rubric-v2` (6 блоков + 4
  штрафа, ADR-026), правило «эталон отсутствует ⇒ обычный 0 через ту
  же бизнес-логику» (не `not_applicable`/`needs_review`), как
  добавить эталон существующему кейсу новой версией сценария, что
  показывает `ItemReview.tsx`/отчёт/CSV/PDF. Убрал устаревшую фразу
  «Оценка 112 остаётся ручной».
- `seed/README.md`: новый раздел «Эталон 112 (`intake112.reference`,
  срез 112-6)» — формат `expected_card`/`address`/`expected_profiles`/
  `alternatives`/`scoring`, правило «эталон отсутствует», правило
  «новая версия того же файла добавляет эталон, не меняя конвейер».
- `slice-planning-112.md`: статус 112-6 в шапке, в таблице среза и в
  §10 обновлён с «план детализирован» на «детерминированный этап
  реализован 26.09.2026»; финальная строка внизу файла указывает на
  фактическую реализацию вместо «следующий шаг».
- `CLAUDE.md`: Current delivery state — 112-6's детерминированный этап
  в списке реализованных срезов, новый пункт с кратким описанием
  ADR-026/правила «эталон отсутствует»; порядок оставшихся срезов
  скорректирован (112-6's LLM-этап вместо всего среза целиком). Code
  map — `internal/assessment/operator112/` и `internal/content/
  normalize` добавлены, уточнено про `rubric.operator112.v1.json`;
  строка про `ItemReview.tsx` дополнена про таблицы блоков/штрафов и
  числовую форму ревизии для 112.
- `slice-112-6-plan.md`: примечание пользователя о будущем прямом
  редактировании поля «Адрес» (вне объёма 112-6) подтверждено на
  месте — строго в конце файла, не тронуто.

Validation: `python3 design-docs/contracts/check.py` (документация не
входит в контракты, но проверка осталась зелёной после всех правок
c1–c10); никакой код в этом коммите не менялся.

Срез 112-6 (детерминированный этап) закрыт: c1 (ADR-026 + контракты) →
c2 (эталон в `internal/content`) → c3 (обобщение конвейера оценки) →
c4 (постановка задачи в training) → c5–c6 (оценщик
`internal/assessment/operator112`) → c7 (интеграционные тесты) → c8
(UI разбора) → c9 (отчётность + правка блокировки экспорта) → c10
(документация). LLM-критерии (`rubric-v3`) и профильная приёмка карт
104/101 (наследие 112-3) остаются открытыми пунктами вне этого среза.

## 2026-09-26 — 112-7: план детализирован, решения пользователя, старт реализации

Пользователь принял решения по срезу 112-7 (`slice-planning-112.md` §11):
(1) предпросмотр проходит сам преподаватель-автор — новое одноразовое
занятие `mode=preview`, участник без рабочего места; (2) предпросмотр
получает автооценку `operator112/rubric-v2`, но она не попадает в
отчёты/историю/прогресс/`trainee_assessment_state`; (3) черновик и его
эталон видит и правит только автор, утверждённая версия доступна всем
преподавателям, чужой сценарий можно только скопировать в свой новый
черновик; (4) редактор в этом срезе поддерживает только
`mode="full_case"` + `caller_mode="free_text"` (ИИ-заявитель) —
авторинг `prepared`-диалога и `card_only` через UI уходит в техдолг
(файловый импорт остаётся для них единственным путём).

Написан [`slice-112-7-plan.md`](slice-112-7-plan.md) (коммиты c1–c10),
`slice-planning-112.md` обновлён (шапка, таблица §3, §11 с пометкой о
сужении объёма, §14 — новый пункт техдолга). ADR-027 оформляется
отдельным коммитом (c1) вместе с контрактами.

Установленные при планировании факты: `scenarios`/`scenario_versions`
уже поддерживают авторство без миграции (draft/approved/superseded,
`created_by`, `source_key` nullable, одна approved на сценарий);
`POST/PUT /scenarios`, `.../approve` уже описаны в openapi.yaml, но не
зарегистрированы в `internal/content/http/handlers.go`;
`content.Validate` сегодня возвращает только первую ошибку — редактору
нужен полный список; `training.checkAssignableVersion` требует
`approved`, `lessons.mode`/`runs.mode` ∈ {intro, training},
`assignments`/`runs` требуют рабочее место (NOT NULL) — миграция под
`preview` меняет это; `enqueueEvaluateWaiting` и отчётность уже
фильтруют по `lesson.Mode`/`lesson_mode`, что даёт готовый шов для
исключения preview.

Next: коммит c1 — ADR-027 и контракты (openapi.yaml пути/схемы,
`schema.sql` + новая миграция под `preview`, `check.py`).

## 2026-09-26 — 112-7 c1: ADR-027 и контракты

[ADR-027](design-docs/adr/027-operator112-scenario-editor.md) —
владение/версии черновика, `PUT`/`approve` с `base_digest`, права
доступа (404 на чужой черновик), каталог 112 для инструктора,
предпросмотр как занятие `mode=preview` (миграция), исключение preview
из оценки статистики/отчётности.

Контракты: `design-docs/contracts/openapi.yaml` — новые пути
`GET /intake112/catalog`, `POST /scenarios/{id}/validate`,
`POST /scenarios/{id}/probe`, `POST /scenarios/{id}/preview-runs`;
переписаны описания `POST /scenarios`, `PUT /scenarios/{id}`,
`POST /scenarios/{id}/approve` (реализованы для 112, ДДС по-прежнему
не реализован); новые схемы `ScenarioEdit`/`ScenarioEditResult`/
`ValidationIssue`/`RubricScoringOverride`; `Intake112Reference`/
`Intake112ExpectedCard`/`Intake112ReferenceAddress` вынесены в именованные
схемы и приведены в соответствие с `internal/content/body.go`
(`expected_profiles`/`alternatives`/`scoring`/`applicant_name`/полный
адрес без `object`/`landmark`) — это исправляет расхождение,
оставшееся с 112-6 c1 (openapi не получил тогда полную форму эталона);
`caller_mode` добавлен в `Intake112Scenario`; `mode` расширен до
`preview` в `Lesson`/`MyRun`/`Item`/`ReportLesson` (не в
`LessonCreate` — `POST /lessons` server-side отклоняет `preview`,
`training.Service.CreateLesson` уже проверяет `Mode != Intro &&
Mode != Training`); новые коды ошибок `stale_draft`/
`has_blocking_issues`/`unsupported_for_editor`.

Миграция `migrations/00017_operator112_preview_lessons.sql`: `preview`
в CHECK `lessons.mode`/`runs.mode`; `workstation_id` nullable на
`runs`/`assignments`; `assignments`' PK → `(lesson_id, user_id)` (PK
столбец нельзя оставить NOT NULL-освобождённым, пока он в PK — пришлось
сначала сменить PK, потом снимать NOT NULL, ошибка обнаружена и
исправлена при живом прогоне миграции против одноразового
Postgres-контейнера); `runs`' `UNIQUE(lesson_id, workstation_id)` →
частичный индекс `WHERE workstation_id IS NOT NULL` (тот же приём, что
`assignments`). `ExpectedSchemaVersion` → 17.
`schema.sql` не тронут — по установленному в 112-1..112-6 прецеденту
(последний раз обновлён в `ff1afa8`, до всех intake-миграций) он не
синхронизируется с каждой миграцией; `check.py`'s DDL-прогон против
него уже падает на main (`reject_immutable_change() does not exist`) —
подтверждено `git stash` до правок, не регрессия этого коммита.

Validation: `go build ./...`, `gofmt -l` — чисто; миграция up/down
проверена вживую на одноразовом `postgres:16-alpine` (не на
compose-стенде проекта) — обе стороны применяются без ошибок, после
проверки возвращена в состояние `up`; `python3
design-docs/contracts/check.py` — зелёный (53 пути, все схемы/примеры);
DDL-часть `check.py` (только при `DATABASE_URL`) не входит в
регрессию — тот же результат на main.

Next: c2 — `internal/content`: `ValidateDetailed` (список issues вместо
первой ошибки), новые проверки авторинга (`unreachable_expected_field`,
`profile_outside_expected_types`, `profile_option_invalid`,
`services_diverge_from_rules`, `ambiguous_ask_pattern`,
`unknown_expected_profile/service/scoring_criterion/alternative_path`,
`unknown_reveal`), чистый классификатор фактов (`probe`) вынесенный из
`aicaller`, unit-тесты.

## 2026-09-26 — 112-7 c2: ValidateDetailed, авторские проверки, вынос классификатора

`internal/content/validate_detailed.go` — `ValidateDetailed(body,
catalog) ([]ValidationIssue, error)`: сперва обычный `Validate` (без
изменений, тем же первым-ошибка поведением для файлового импорта);
если тело уже структурно валидно и это `operator112_intake` —
`collectIntake112AuthoringIssues` добавляет к нему многократные
проверки из ADR-027: `unreachable_expected_field` (эталонное поле,
которое ни один факт free_text-диалога не может раскрыть — `initial`
всегда достижимо, `on_question` только если есть `ask_patterns`;
`country/region/okrug` — warning, остальное — error),
`profile_outside_expected_types` (карта в `expected_profiles` не
входит ни в один `expected_types` через `intake-catalog.json`'s
`types[].profile_ids`), `services_diverge_from_rules` (warning;
пересчитывает `service_rules` по `expected_types`/`expected_profiles`
и сравнивает с `expected_services`, пропуская правило с
неотвеченным `field_id` — не гадает), `ambiguous_ask_pattern`
(warning; два факта делят один и тот же `ask_patterns[0]`). Часть
кодов из плана (`unknown_reveal`, `contradicts_fact`,
`profile_option_invalid`, `unknown_scoring_criterion` и т.д.) уже
блокируют структурную проверку внутри `Validate` — коллектор их не
дублирует, они просто не могут дойти до этой стадии.

Классификатор фактов (`FactAsked`/`AskedFacts`/`DiscloseReveals`,
раньше — `internal/training/operator112/aicaller/classify.go`)
перенесён в `internal/content/intake_classify.go` без изменения
поведения — нужен и будущему `/probe` (c3, чистая функция без модели),
и уже существующему `aicaller`; `content` ниже `training` по слоям,
поэтому канонический дом — здесь. `aicaller/classify.go` стал тонким
форвардером (`FactAsked`/`AskedFacts`/`DiscloseReveals` вызывают
`content.*`), добавлен `content.MatchesPattern` для `replier.go`'s
`scriptedAnswer`. Вызывающий код и тесты `aicaller` не менялись.

Тесты — `internal/content/validate_detailed_test.go`: структурная
ошибка даёt один issue (регрессия); чистый сценарий не даёт issues;
недостижимое поле (error) и его понижение до warning для
country/region/okrug; поле, достижимое через `ask_patterns`, не
считается недостижимым; карта вне `expected_types`; расхождение
`expected_services` с `service_rules` (и обратный случай — совпадение
не даёт warning); неоднозначный `ask_patterns[0]`.

Validation: `go build ./...`, `gofmt -l` — чисто; `go test
./internal/...` — весь пакет зелёный (включая `aicaller` и
`assessment/operator112`, не задетые рефакторингом).

Next: c3 — `internal/content`: create/copy/save (с версионированием
черновика по `base_digest`)/approve, права владения (404 на чужой
черновик), `GET /intake112/catalog`, HTTP-хендлеры, audit.

## 2026-09-26 — 112-7 c3–c4: сервис редактора, HTTP, предпросмотр-занятие

Status: completed
Slice: 112-7

Changed:
- `internal/content/editor.go` (new): `EditorScenario`/`ScenarioCreateInput`/`ScenarioEditInput`, `CreateOperator112Scenario` (новый черновик или копия чужой approved-версии), `SaveOperator112Draft` (правка на месте, пока черновик не используется предпросмотром; иначе версия N+1 и `superseded` для прежней — оптимистичная блокировка по `base_digest`, `stale_draft` при конфликте), `ValidateOperator112Draft`/`ProbeOperator112` (используют перенесённый в c2 классификатор), `ApproveOperator112Scenario` (переводит прежнюю `approved` в `superseded` в той же транзакции), `IntakeCatalogForInstructor`. Права владельца — `scenarios.created_by`; чужой черновик всюду отдаёт `ErrNotFound`, чужой approved можно только копировать (`ErrUnsupportedForEditor` для не-`full_case`/`free_text`).
- `internal/content/store.go`, `internal/content/postgres/store.go`: `SupersedeVersion`/`ApproveVersion`/`UpdateScenarioStatus`/`UpdateScenarioTitle`.
- `internal/content/http/editor_handlers.go` (new) + `handlers.go`: `POST /scenarios`, `PUT /scenarios/{id}`, `POST /scenarios/{id}/validate`, `POST /scenarios/{id}/probe`, `POST /scenarios/{id}/approve`, `POST /scenarios/{id}/preview-runs`, `GET /intake112/catalog`; `GET /scenarios/{id}` теперь при `ErrNotFound` от обычного пути дополнительно пробует `EditorScenarioDetail` владельца (черновик виден только автору тем же эндпоинтом, который отдаёт approved всем).
- `cmd/emsim/api.go`: `contenthttp.NewHandlers` получил `training.Service` как `previewStarter` (по образцу `assessmenthttp`/`reportinghttp` — кросс-модульная зависимость только на транспортном слое, не в домене `content`).
- `migrations/00017_operator112_preview_lessons.sql` + `ExpectedSchemaVersion=17`: `preview` в `lessons_mode_check`/`runs_mode_check`; `runs.workstation_id`/`assignments.workstation_id` nullable; `assignments` PK → `(lesson_id, user_id)` + частичный `UNIQUE(lesson_id, workstation_id) WHERE workstation_id IS NOT NULL`; проверено up/down на разовом контейнере `postgres:16-alpine` (не dev-стек проекта), контейнер удалён после проверки.
- `internal/training/domain.go` (`ModePreview`), `internal/training/ports.go` (`ScenarioReader.ScenarioByID`), `internal/training/preview.go` (new): `Service.StartPreview` одной транзакцией останавливает предыдущий активный preview автора (штатный `Stop`), проверяет владение и `operator112_intake`+`Intake112!=nil`, строит `lessons`/`assignments`/`runs`/`items` напрямую через store-методы — **не** через `ReplaceAssignments`/`Start`/`checkAssignableVersion` (see ADR-027's "уточнение по факту реализации": обычный путь назначения по-прежнему требует `approved` без исключений, предпросмотр просто не проходит через него).
- `internal/training/store.go`: `workstationMatches(actor, run) bool` — `true` без сверки РМ для `run.Mode==preview`, иначе прежнее сравнение; заменил 4 дублированных проверки в `service.go` (`Execute`, get-item, upload-recording, voice-phrase — все места, где раньше был инлайн `actor.WorkstationID==nil||...`).
- `internal/training/service.go`: `offerQueueVersion` возвращает `(uuid.UUID, error)` (новый item id нужен `StartPreview`), 4 вызывающих места обновлены.
- `internal/auth/authz.go`: новая `GroupItemActions` (`{instructor, trainee}`) — `POST /items/{id}/actions` было `GroupTrainee`-only, что блокировало маршрут для инструктора-автора predпросмотра ещё до вызова `Execute` (которая уже готова: `run.UserID==actor.UserID` проверяется независимо от роли, `workstationMatches` покрывает preview). `GET /items/{id}` новой группы не потребовал — `GroupItemRead` уже пускал обе роли, а `ItemForInstructor` проверяет `lesson.InstructorID==actor.UserID`, что для собственного preview-занятия автора уже истинно.
- `internal/content/http/handlers_test.go`: `TestScenarioDetailNotFoundMapsTo404` донастроен (`editorDetailErr` тоже `ErrNotFound` — иначе тест давал ложный зелёный на новом fallback-пути), добавлен `TestScenarioDetailFallsBackToOwnerDraftOnNotFound`.
- `internal/training/http/handlers_test.go`: `TestRoutesRequireAuthenticationAndCorrectRoles` — `POST .../actions` вынесен из общего `traineeRoutes` (там инструктор теперь легитимен), добавлен `TestPreviewInstructorReachesItemActions` (маршрутный уровень; предметную проверку владения делает существующий `TestOwnershipAndWorkstationErrors`/`service_test.go`).
- `design-docs/adr/027-operator112-scenario-editor.md`: добавлено уточнение по факту реализации (`checkAssignableVersion` не расширялась исключением — `StartPreview` её не вызывает; авторизация команд реализована `GroupItemActions`, а не веткой внутри хендлера).

Decisions:
- Зафиксировано расхождение между ADR-027 (черновик плана: "checkAssignableVersion получает исключение") и реализацией (StartPreview обходит её целиком) — задокументировано в самой ADR как уточнение, а не тихо исправлено.

Validation: `go build ./...`, `go vet ./...`, `gofmt -l .` — чисто; `go test ./internal/...` — весь пакет зелёный; `python3 design-docs/contracts/check.py` — все проверки прошли (DDL-шаг пропущен, `DATABASE_URL` не задан).

Next: c5 — `enqueueEvaluateWaiting` для `ModePreview`; пропуск версии `trainee_assessment_state` для preview (`internal/assessment`); фильтр `lesson_mode != 'preview'` везде, где уже фильтруется `lesson_mode != 'intro'` (`internal/reporting/postgres/store.go`).

## 2026-09-26 — 112-7 c5: preview получает реальную оценку, но не влияет на статистику

Status: completed
Slice: 112-7

Changed:
- `internal/training/service.go`: `enqueueEvaluateWaiting` теперь ставит `assessment.evaluate` для `lesson.Mode ∈ {ModeTraining, ModePreview}` (было только `ModeTraining`) — preview получает настоящую rubric-v2 auto-оценку, видимую автору через существующий `GET /items/{id}/assessment`.
- `internal/assessment/service.go`: `CreateExpertRevision` пропускает `store.BumpTraineeStateVersion` при `item.Mode == training.ModePreview` — единственное место, где вообще двигается версия основания рекомендаций (сам auto rev=1 её не трогает); preview-оценка никогда не станет основанием будущей рекомендации.
- `internal/training/postgres/store.go`: `ListLessonsByInstructor` получил безусловный `AND mode != 'preview'` — без него одноразовые preview-занятия захламляли бы `GET /lessons` вперемешку с настоящими.
- `design-docs/adr/027-operator112-scenario-editor.md`: уточнение по факту реализации — `lesson_report_rows`'s `JOIN workstations` (не `LEFT JOIN`) уже молча исключает любой preview-run (workstation_id всегда NULL там, миграция 00017), так что `LessonReport`/CSV/PDF/`ResultsFor`/`ProgressFor` не потребовали ни одной правки SQL; `/users/{id}/progress` и `/groups/progress` из RFC-001 §5 в коде не реализованы вовсе, фильтровать нечего.

Decisions:
- Первоначальный план ADR-027 предполагал добавить `lesson_mode != 'preview'` фильтр в несколько мест `internal/reporting`; по факту это уже гарантировано схемой (INNER JOIN на nullable `workstation_id`) везде, кроме списка занятий, который читает `lessons` напрямую, а не `lesson_report_rows`. Задокументировано в ADR, а не исправлено тихо.

Validation: `go build ./...`, `go vet ./...` — чисто; `go test ./internal/...` — весь пакет зелёный (без новых unit-тестов: `enqueueEvaluateWaiting`/`CreateExpertRevision`/SQL-исключение через JOIN — не покрыты фикстурами в этом модуле уже сегодня, проверяются интеграционно; c6 добавит сквозной прогон preview→auto-оценка→отсутствие в отчётах); `python3 design-docs/contracts/check.py` — все проверки прошли.

Next: c6 — интеграционные тесты против настоящего PostgreSQL: полный цикл create→edit→validate→preview→auto-assessment→approve→assign→trainee-run; конкурентный PUT на один черновик; проверка, что preview-lesson реально пропадает из `lesson_report_rows`/`GET /lessons`, а `trainee_assessment_state.version` не растёт.

## 2026-09-26 — 112-7 c6: интеграционные тесты редактора и предпросмотра против PostgreSQL

Status: completed
Slice: 112-7

Changed:
- `test/integration/operator112_editor_test.go` (new, `//go:build integration`):
  - `TestOperator112EditorVersionLifecycleAgainstRealDatabase` — `CreateOperator112Scenario`/`SaveOperator112Draft`/`ApproveOperator112Scenario` против реальной БД: создание v1 draft; чужой инструктор получает `ErrNotFound` и на `EditorScenarioDetail`, и на `SaveOperator112Draft`; сохранение с устаревшим `base_digest` — `ErrStaleDraft`, без побочных эффектов; корректное сохранение создаёт v2 и переводит v1 в `superseded` (подтверждено прямым SQL-чтением статуса) — эмпирически проверяет ADR-027's "каждый PUT создаёт новую версию"; утверждение со старым digest отклоняется, с текущим — проходит; копия чужого утверждённого сценария создаёт новый scenario_id с `created_by` = копирующий.
  - `TestOperator112PreviewRunEndToEndExcludedFromStatistics` — `insertOwnedOperator112Version` кладёт тело реального seed-сценария (`pilot-112-full-gas-road-traffic-fire-01`) как версию, принадлежащую тестовому инструктору (ни один файловый seed не принадлежит обычному инструктору, только импортирующему admin), затем `StartPreview` → проверка `lessons.mode='preview'` и `runs.workstation_id IS NULL` прямым SQL; инструктор проходит свой же item тем же путём, что и обычный `closeFullCaseItem` (`ItemForTrainee`/`Execute` не различают роль — только `run.UserID`/`workstationMatches`); close реально ставит `assessment.evaluate` → coordinator → `claimAndHandle` даёt один `auto rev=1 ready` (переиспользованы `newIntake112AssessmentServiceForTest`/`mustRunCoordinatorTick`/`claimAndHandle`/`readAssessments` из `operator112_assessment_pipeline_test.go`); `ListLessons` для этого инструктора не содержит preview-lesson, но содержит его же обычный `f.lesson`; `SELECT count(*) FROM lesson_report_rows WHERE item_id=$1` = 0 (эмпирическое подтверждение ADR-027's находки про `INNER JOIN workstations`); экспертная ревизия (`CreateExpertRevision`, критерии через уже существующий `resolveAllCriteria`) не двигает `trainee_assessment_state.version` (читается напрямую SQL, отсутствие строки трактуется как версия 0).
  - Мелкие переиспользуемые хелперы: `unmarshalScenarioBody` (тело сценарного JSON → `content.Body`), `insertOwnedOperator112Version` (аналог уже существующего `insertLegacyIncomingCallVersion`, но с параметризуемым владельцем), `traineeStateVersion`.

Decisions:
- Для проверки предпросмотра сознательно взято тело реального `prepared`-диалогового seed-сценария (а не `full_case`+`free_text`, единственный формат, который создаёт сам редактор), напрямую вставленное в БД с `created_by`=инструктор — `StartPreview` в коде не требует `caller_mode="free_text"`, только `exercise_type=operator112_intake && Intake112≠nil`, так что это честно проверяет именно preview-инфраструктуру (лишённую воркстейшна, изолированную от отчётности), не задваивая уже покрытую editor-only-eligibility unit-тестами (`editor_test.go`) логику "редактор создаёт только full_case+free_text".

Validation: собственно новый файл — `go build -tags=integration ./test/integration/...`, `go vet -tags=integration ./test/integration/...` — чисто; запуск против разового `postgres:16-alpine` контейнера (`TEST_DATABASE_URL`, не dev-стек проекта, контейнер удалён после прогона) — оба новых теста **PASS**, затем весь `go test -tags=integration -count=1 ./test/integration/...` (66 c) — **PASS**, включая уже существующие 112/DDS сценарии, т.е. без регрессий. Дополнительно: `make verify` (format-check, build, test, vet, staticcheck) — чисто; `python3 design-docs/contracts/check.py` — все проверки прошли.

## 2026-09-26 — 112-7 c7 prep: два бэкенд-пробела, найденные при старте web UI

Status: completed
Slice: 112-7

Changed:
- `internal/training/http/handlers.go`: `traineeIntakeState` больше не возвращает `available_service_codes` — эту часть вынесена в отдельную `availableServiceCodesForState`, которую `toItemJSON` теперь вызывает **для обеих** веток (инструктор и обучаемый), а не только для `traineeView`. Причина: `web/Operator112ProfileCase.tsx` (переиспользуется для предпросмотра 112-7 без изменений) читает `item.available_service_codes` в модалке «Список оповещаемых служб» для ручного добавления/снятия службы; раньше это поле оставалось `nil` для инструктора, и предпросмотр мог использовать только автоматически предложенный список, без ручной правки — расхождение с обычным прохождением обучаемого. Список кодов служб не секретен (в отличие от самих `service_rules` с условиями), поэтому его показ инструктору не нарушает никакой границы. `internal/training/http/intake_projection_test.go` и новый `TestOperator112InstructorItemGetsAvailableServiceCodesToo` (`handlers_test.go`) проверяют это явно.
- `internal/content/store.go`, `internal/content/postgres/store.go`, `internal/content/http/handlers.go`: `GET /scenarios` (`ListScenarios`) раньше `JOIN scenario_versions sv ON sv.status='approved'` — обычный INNER JOIN, из-за которого сценарий без единой утверждённой версии **не мог появиться в списке вообще ни для кого**, включая собственного автора при фильтре `status=draft` (этот фильтр в UI уже существовал, но был мёртвым). Добавлено `ScenarioFilter.RequestingUserID` (всегда ставится HTTP-слоем из `authhttp.PrincipalFromContext`, никогда не приходит от клиента); SQL заменён на `LEFT JOIN LATERAL`, который берёт approved-версию как раньше, а при её отсутствии — последнюю версию сценария, но только если `s.created_by = RequestingUserID`. Так план 112-7 «фильтр «Мои черновики»» реализован переиспользованием уже существующего `status=draft` в `ScenarioCatalogueRoute` (новый отдельный переключатель не нужен) — решение зафиксировано здесь, а не в новом UI-элементе.

Decisions:
- «Мои черновики» — это существующий `status=draft` фильтр, а не новый элемент интерфейса: черновик по ADR-027 в принципе виден только автору, поэтому «показать черновики» и «показать мои черновики» — один и тот же запрос после этого исправления.

Validation: `go build ./...`, `go vet ./...`, `gofmt -l` — чисто; `go test ./internal/...` — весь пакет зелёный. Новый интеграционный тест `TestScenarioListSurfacesOwnDraftsOnlyToTheirAuthor` (`test/integration/operator112_editor_test.go`) плюс полный `go test -tags=integration -count=1 ./test/integration/...` (67 с) против разового `postgres:16-alpine` (удалён после прогона) — всё **PASS**, без регрессий в остальных сценариях каталога/112.

Next: c7–c9 — web UI: `api/content.ts` хуки (`useCreateScenario`/`useSaveScenario`/`useApproveScenario`/`useValidateScenario`/`useProbe`/`useIntakeCatalog`), исправление предсуществующего риска падения `ScenarioDetail.tsx` (безусловное обращение к `call.aon`/`call.script.map` на кейсах без `call`/`script`), 5-вкладочный редактор, экран предпросмотра (переиспользование `Operator112ProfileCase`/`CallerChat`), вынос панели авто-оценки из `ItemReview.tsx` в общий компонент, `operator112-editor.spec.ts` e2e. Перед началом — прочитать текущий `web/src/routes/instructor/*` и `web/src/api/content.ts`, чтобы не дублировать уже существующие паттерны хуков/роутинга.

## 2026-09-26 — 112-7 c7–c10: веб-редактор, предпросмотр, e2e и документация — срез завершён

Status: completed
Slice: 112-7

Changed:
- `web/src/api/content.ts`: `Intake112EditorBody`/`emptyIntake112Body` (единственная форма, которую редактор создаёт — `full_case`+`free_text`, включая обязательные, но всегда-пустые `dialogue.initial`/`questions` для соответствия общей wire-схеме), `EditorScenario`, `useIntake112Catalog`, `useCreateScenario`/`useSaveScenario`/`useValidateScenario`/`useProbeScenario`/`useApproveScenario`/`useStartPreviewRun`.
- `web/src/routes/instructor/ScenarioEditor.tsx` (new): `/instructor/scenarios/new` и `/:scenarioId/edit` — один компонент с переключателем вкладок (Общее/Заявитель/Факты/Эталон/Проверка) вместо пяти отдельных маршрутов; «Создать черновик» создаёт сценарий и переключает URL на `/edit`; «Проверить»/тестер фраз/«Пройти самому»/«Утвердить» — прямые вызовы соответствующих мутаций.
- `web/src/routes/instructor/ScenarioPreview.tsx` (new): `/instructor/preview/:itemId` — тонкая обёртка вокруг `useItem`+`Operator112ProfileCase` (переиспользован без изменений: обычный `GET/POST /items/{id}...` не различает роль, только `run.user_id`/`workstationMatches`, уже готовые в c4). `lessonId` передаётся через `navigate(..., {state})`, чтобы показать кнопку «Завершить предпросмотр» (`POST /lessons/{id}/stop`) — необязательна: следующий `StartPreview` того же автора всё равно останавливает предыдущий прогон сам.
- `web/src/components/IntakeAutoAssessment.tsx` (new): панель блоков/штрафов rubric-v2, вынесенная из `ItemReview.tsx` (там теперь просто импортируется) — используется предпросмотром 112-7 один в один с разбором преподавателя.
- `web/src/routes/instructor/ScenarioDetail.tsx`: исправлен реальный краш на `full_case`/`free_text` сценариях (`intake.call.script.map`/`reference.expected_card.age` на `undefined`) — сепарирован `Operator112ScenarioDetail`, который читает `GET /scenarios/{id}`'s собственное тело (не отдельный `/preview`, который 404-ит на неутверждённом черновике), показывает факты/эталон по фактической форме intake112 и добавляет кнопки Копировать/Редактировать/Предпросмотр/Утвердить с проверкой владения на клиенте (`created_by`).
- `web/src/routes/instructor/ScenarioCatalogue.tsx`: кнопка «+ Создать сценарий 112».
- `web/src/api/errors.ts`: тексты для `stale_draft`/`has_blocking_issues`/`unsupported_for_editor`.
- `web/src/App.tsx`: три новых маршрута.
- **Реальный бэкенд-баг, найденный через e2e в реальном браузере** (не ловится `tsc`/`lint`/юнит-тестами с моками): `internal/content/http/handlers.go`'s `toValidationIssuesJSON` возвращал `nil` при отсутствии замечаний → JSON `"issues": null` вместо `[]` для полей, объявленных в `openapi.yaml` как `required` (`ScenarioEditResult`, ответ `/validate`) — `issues.filter(...)` в `ScenarioEditor.tsx` падал на `null`, крашя всё SPA (пустой экран, React root размонтирован) в момент клика «Пройти самому» после «Проверить». Исправлено: `toValidationIssuesJSON` всегда возвращает не-nil (возможно пустой) срез — `omitempty` у `scenarioJSON.Issues` по-прежнему опускает пустой срез, так что владелец-only fallback не меняет поведение. Плюс defensive `?? []` в двух местах `ScenarioEditor.tsx`, которые раньше не подстраховывались.
- Попутно (обнаружено при чтении `Operator112ProfileCase.tsx` для переиспользования в предпросмотре): `internal/training/http/handlers.go`'s `available_service_codes` раньше вычислялся только для `traineeView` — инструктор на своём предпросмотре не мог вручную скорректировать список служб в модалке «Список оповещаемых служб» (мог только принять автопредложение). Вынесено в отдельную `availableServiceCodesForState`, вызывается для обеих ветвей; `openapi.yaml`'s описание поля обновлено.
- `design-docs/contracts/openapi.yaml`: `ScenarioSummary.created_by` (не секретно — утверждённый сценарий и так виден всем инструкторам; нужно клиенту для решения «показывать ли Редактировать»), `Scenario.version_status`/`.issues` (уже существовали в ответе `GET /scenarios/{id}`, но не были описаны в контракте — исправлена документационная рассинхронизация), исправлено устаревшее описание `PUT /scenarios/{id}` («правка на месте» — на самом деле всегда новая версия, ADR-027).
- `README.md` (новый раздел «Редактор сценариев 112 (срез 112-7)»), `seed/README.md` (раздел «Редактор 112-7 и файловый импорт»), `CLAUDE.md` (новый пункт про 112-7 в Current delivery state, обновлены Code map/remaining order), `slice-planning-112.md` (шапка, таблица §3, статус §11, финальный абзац), `slice-112-7-plan.md` (статус «реализован», отметка по коммитам, уточнение по факту реализации про `expected_profiles`/`alternatives`/`scoring` без отдельного UI).

Decisions:
- «Мои черновики» реализовано переиспользованием существующего `status=draft` фильтра (задокументировано в предыдущей записи), а не новым UI-элементом — здесь только подтверждение, что каталог с этим фильтром действительно показывает свежесозданный черновик после исправления `ListScenarios`.
- `expected_profiles`/`alternatives`/`scoring` не получили отдельного UI в редакторе (только `expected_types`/`case_description`/`expected_services`/`expected_card`) — не блокирует ни один пункт Definition of Done, задокументировано как явное сужение в `slice-112-7-plan.md` §8, а не тихо опущено.

Validation: `go build ./...`, `go vet ./...`, `gofmt -l` — чисто; `go test ./internal/...` — весь пакет зелёный; `npx tsc -b` и `npm run lint` (web) — чисто (только 4 pre-existing/безопасных warning'а); `npm run build` (web) — успешно. `python3 design-docs/contracts/check.py` — все проверки прошли. Интеграционный прогон против разового `postgres:16-alpine` — без регрессий. **e2e против реального docker-compose стека** (`npm run test:e2e`, сборка образа с обоими бэкенд-фиксами): новый `web/e2e/operator112-editor.spec.ts` (создание через UI → факты/эталон → «Создать черновик» → «Проверить» без блокирующих ошибок → «Пройти самому» → реальное прохождение ARM-интерфейса до «Кейс завершён» → «Завершить предпросмотр» → подтверждение отсутствия в списке занятий → «Утвердить» → появление в конструкторе занятия для назначения) и все 6 существующих спеков (`operator112.spec.ts` ×4, `operator112-profiles.spec.ts`, `smoke.spec.ts`) — **7/7 PASS**, без регрессий.

Next: срез 112-7 полностью завершён (c1–c10). Следующий шаг по `slice-planning-112.md` — 112-5b этап 2 (замер W0, `llama-server`/GGUF в compose) либо LLM-критерии rubric-v3, либо 112-8/112-9 — по решению пользователя.

## 2026-09-26T19:24:29+03:00 — Claude (Sonnet 5) — 112-6 LLM-этап: DESCRIPTION_CONTENT LLM-судья (ADR-028, c1–c12)

Status: completed
Slice: 112-6 (LLM-этап, поверх реализованного детерминированного этапа)

Changed:
- `design-docs/adr/028-operator112-description-llm-judge.md`, `slice-112-6-llm-plan.md` (новые).
- `design-docs/contracts/`: `rubric.operator112.v3.json` (новый, rubric-v2 + DESCRIPTION_CONTENT); `rubric.operator112.json` (v2) не менялся; `rubric.schema.json` (+`card_description`), `scenario.schema.json` (`description_questions`), `assessment-inputs.schema.json` (`exercise_type` — оба типа), `openapi.yaml` (`Intake112Reference.description_questions`, `CriterionDetail.status`+=`unavailable`), `embed.go`, `check.py`.
- `internal/assessment/http/handlers.go` (+тест): исправлен реальный баг — `criterionJSON` не нёс `penalty_points`/`details` ни на вход, ни на выход (панель «Подробности» никогда не заполнялась, штрафные баллы эксперта терялись при декодировании).
- `internal/content`: `Intake112Reference.DescriptionQuestions`, валидация, `operator112RubricCriterionIDs` — объединение id v1+v2+v3, `Operator112RubricVersion(judgeEnabled)`, предупреждение `no_description_questions` в `ValidateDetailed`.
- `internal/platform/llm`: `Request.ResponseFormat`. `internal/platform/config`: `ASSESSMENT_JUDGE` (api и worker), `JUDGE_LLM_URL`/`JUDGE_LLM_MODEL`/`JUDGE_TIMEOUT`/`JUDGE_MAX_TOKENS`.
- `internal/assessment`: `RuleEvaluator.Evaluate`+=`SemanticAnswers`; новые `SemanticPreparer`/`SemanticRequest`/`SemanticJudge`/`SemanticJudgeRegistry`/`JudgeConfig`; `Service.judge`; `Handle` разбит на `answerSemantic` (вызов судьи вне транзакции, `HandlerFailure(Retryable,"judge_unavailable")` при сбое) + `recordAutoTx`; `computeAndInsertAuto` принимает ответы судьи, пишет `Assessment.Model`; `FinalizeExpired` передаёт `nil` (исчерпание → `needs_review`, не ноль); `rubric.go` знает `operator112/rubric-v3`.
- `internal/assessment/operator112`: `descriptionContentRule` (DESCRIPTION_CONTENT, 10 баллов поровну между вопросами; needs_review у любого вопроса → весь критерий `unavailable`), `PrepareSemantic` (запрос судье только при вопросах+заполненном описании, по снимку оповещения). Новый пакет `internal/assessment/operator112/descjudge` — промпт/схема/разбор дословно из архива пользователя, адаптер над `internal/platform/llm`.
- `internal/training`: `Service.operator112JudgeEnabled`, `rubricVersionForNewLesson` — выбор `rubric-v2`/`rubric-v3` при создании занятия/предпросмотра.
- `cmd/emsim`: `judgeConfigFor` (worker), `ASSESSMENT_JUDGE` в обоих процессах, `newTrainingService`/`newAssessmentService` получили судью.
- `compose.yaml`: `ASSESSMENT_JUDGE`/`JUDGE_*` (default off/безопасные значения).
- `seed/scenarios/pilot-112-ai-{car-in-water,mobile-shop,toyota-fire}-01-v2.json` (новые версии 2) — вопросы из архива; версии 1 не менялись.
- `internal/reporting/postgres/store.go` (+тест) — `DESCRIPTION_CONTENT` в той же колонке отчёта, что `DESCRIPTION_PRESENT`.
- `web`: `ScenarioEditor.tsx` (вкладка «Эталон» → вопросы к описанию), `ScenarioDetail.tsx` (read-only вопросы), `ItemReview.tsx` (модель судьи), `api/content.ts` (`Intake112DescriptionQuestion`). `IntakeAutoAssessment.tsx` не менялся — уже универсален.
- `test/integration/operator112_description_judge_test.go` (новый), `operator112_assessment_pipeline_test.go` (варианты фикстур с судьёй), `content_import_test.go` (обновлены счётчики импорта сидов: 15/19 версий вместо 15/16 — регрессия от новых сидов, не от логики).
- `web/e2e/operator112.spec.ts` — исправлена ставшая видимой e2e-регрессия от фикса `criterionJSON`: локатор строки блока по всему тексту `tr` стал находить два совпадения (CALLER_TOPICS перечисляет в «Подробности» тему с меткой «Адрес происшествия», совпадающей с заголовком блока ADDRESS_FIELDS); сужен до первой ячейки строки.
- `README.md`, `CLAUDE.md`, `slice-planning-112.md`, `seed/README.md` — задокументирован срез и отступление от порядка плана (LLM-этап 112-6 до этапа 2 112-5b, по решению пользователя).

Decisions:
- Начать LLM-этап 112-6 до замера W0/этапа 2 112-5b — решение пользователя 26.09.2026, зафиксировано в ADR-028 (отступление от `slice-planning-112.md`).
- 10 баллов делятся поровну между вопросами сценария; любой `needs_review` — весь критерий `unavailable`; нет вопросов/пустое описание — 0 без вызова модели; правка эксперта — баллами за критерий целиком (без правки по вопросам); новое занятие получает `rubric-v3` только при `ASSESSMENT_JUDGE=llm` в конфигурации.
- Судья вызывается вне транзакции (`answerSemantic`), результат сворачивается в тот же вызов `computeAndInsertAuto` — без отдельной задачи/таблицы для судейского ответа (в отличие от `caller.reply`, здесь одна попытка задачи = один вызов модели = одна запись).
- Вопросы добавлены только трём сидам, совпадающим с кейсами архива; остальные 19 сценариев архива — вне объёма, отдельная задача.
- `rubric.operator112.json` (v2) не переименован и не тронут — новая рубрика в отдельном `rubric.operator112.v3.json`, чтобы не переопределять уже согласованный смысл «текущей» версии v2 для существующих потребителей (`content.RubricVersionFor`, `TestRubricVersionForOperator112IsV2`).

Validation:
- `make verify` — чисто (build/test/vet/staticcheck).
- `make verify-web` (`npx tsc -b`, `npm run build`) — чисто; `npm run lint` — только 4 pre-existing warning'а (не мои).
- `python3 design-docs/contracts/check.py` — все проверки прошли.
- `make test-integration` (полный набор, реальный `postgres:16-alpine` через testcontainers) — **ok, 202.7s**, без регрессий (после исправления жёстко заданных счётчиков импорта сидов в `content_import_test.go`).
- `cd web && npm run test:e2e` — **7/7 PASS** (после исправления локатора строки, см. Changed).
- Живая проверка на реальной модели (`ollama serve`, `t-tech/T-lite-it-2.1:q5_K_M`): `go test -tags=llmlive ./internal/assessment/operator112/descjudge/...` — структурированный JSON-ответ через `ResponseFormat` разобран без ошибок на всех 5 непустых кейсов (car-in-water/mobile-shop/toyota-fire, correct+missing_fact), 15/16 ожидаемых ответов совпали (одно расхождение — реальное суждение модели на неоднозначной формулировке, не сбой реализации). Ollama остановлена после проверки (`pkill -f "ollama serve"`).

Open issues / risks:
- Полный сквозной прогон через реальный `docker compose` с `ASSESSMENT_JUDGE=llm` (а не только юнит/интеграционные тесты с фейковым судьёй + отдельный живой прогон адаптера) не выполнялся — риск низкий: интеграционные тесты покрывают весь конвейер транзакций с фейковым судьёй, живой тест — реальный HTTP/JSON-schema транспорт с той же моделью.
- 19 сценариев архива без description_questions — тех. долг, явно вне объёма.
- Замер W0 (112-5b этап 2) по-прежнему не проведён; ёмкость судьи на целевом сервере неизвестна.

Next: 112-5b этап 2 (замер W0, `llama-server`/GGUF в compose) остаётся следующим крупным шагом по плану; либо LLM-проверка содержательности разговора (`CALLER_TOPICS`) как продолжение этой же работы, либо 112-8/112-9 — по решению пользователя.

## 2026-09-27 — исправления по ревью 112 (п. 1, 2, 3, 5); п. 4, 8 — техдолг

Источник: `docs/review-operator112-2026-09-26.md` (ревью другой модели на `9c1a660`; его строка «rubric-v3 не реализован» устарела — LLM-этап 112-6 уже в `2a05379`). Решение пользователя 27.09: исправить п. 1, 2, 3, 5 отдельными коммитами; п. 4 (структурная проверка редактора) и п. 8 (`base_digest` не защищает название) — в техдолг (`slice-planning-112.md` §14); п. 6, 9, 10, 11 согласованы, но ещё не сделаны; п. 7 (эталон профильных карт в редакторе) — позже.

Changed:
- п. 1: `content.readVersionForCopy` — копировать можно только свою версию или опубликованную (`approved_at`); чужой черновик — `ErrNotFound`. `ScenarioVersions(ctx, actorID, id)` — не-автор видит только опубликованные версии, сценарий без них — 404. ADR-027 («Копирование», «Права доступа») поправлен: прежний текст утверждал, что фильтр уже есть.
- п. 2: `ScenarioPreview.tsx` подписан на `/lessons/{id}/stream`; id занятия передаётся в `?lesson=` (редактор и карточка сценария). e2e редактора теперь отправляет сообщение заявителю и ждёт ответа без перезагрузки.
- п. 3: `callerTurnsWait` учитывает ожидание ответа только внутри `[answered_at, notified_at]` (с обрезкой, незавершённый ход — до конца окна). ADR-026, строка T_FILL уточнена.
- п. 5: `add_incident_type`/`remove_incident_type` отклоняются при `call_status=ringing` (как `save_intake_draft`).

Validation: `make verify` — ok; `make verify-web`, `npm run lint` — ok (4 прежних предупреждения); `make test-integration` (после п. 1) — ok, 183 с; `npm run test:e2e` — 7/7.

Next: п. 6 (серверная валидация версии перед preview), п. 9 (лимиты в символах, не байтах; не терять текст отклонённого сообщения), п. 10 (оценка на экране предпросмотра), п. 11 (редактор не должен затирать несохранённые правки при фоновом refetch).

## 2026-09-27 — исправления по ревью 112 (п. 6, 9, 10, 11)

Продолжение предыдущей записи. Все согласованные пункты ревью `docs/review-operator112-2026-09-26.md` закрыты; остаются п. 4 и 8 (техдолг, §14 `slice-planning-112.md`) и п. 7 (эталон профильных карт в редакторе — позже).

Changed:
- п. 6: `content.Service.CheckPreviewable` — валидирует сохранённую версию (`ValidateDetailed`, владелец, версия своего сценария); `POST /scenarios/{id}/preview-runs` вызывает его до `training.StartPreview`, поэтому при блокирующих ошибках (`422 has_blocking_issues`, как уже обещал OpenAPI) прежний предпросмотр не останавливается и занятие не создаётся. В редакторе кнопка предпросмотра неактивна при ошибках, как «Утвердить».
- п. 9: все серверные лимиты длины текста считают символы (`utf8.RuneCountInString`), а не байты: чат (500), поля карточки 112 (1000/1999/100), текстовые ответы профильных карт (1999), причина правки служб (1000), причина остановки занятия (500), причина экспертной ревизии (3..2000). `CallerChat` считает code points, блокирует превышение вместо обрезки и очищает поле только когда реплика появилась в разговоре; при отклонении текст остаётся, причина показывается в чате.
- п. 10: `ScenarioPreview.tsx` показывает автооценку предпросмотра (состояние задачи, итог, панель `IntakeAutoAssessment`) и ссылку на полный разбор.
- п. 11: `ScenarioEditor.tsx` загружает форму с сервера один раз на сценарий; фоновый refetch её не перезаписывает. Если на сервере появилась версия, которую форма не загружала и не сохраняла, показывается предупреждение с явной кнопкой загрузки.

Validation: `make verify` — ok; `make verify-web`, lint — ok (4 прежних предупреждения); `make test-integration` — ok, 187 с; `python3 design-docs/contracts/check.py` — ok; `npm run test:e2e` — 7/7 (e2e чата отправляет 300 кириллических букв; e2e редактора ждёт автооценку на экране предпросмотра и проверяет, что несохранённое название переживает refetch по фокусу).

Risks: коммит п. 10 отдельно e2e не прогонялся (итоговое дерево после п. 11 — прогонялось целиком).

Next: техдолг п. 4/8 по решению пользователя; п. 7 (эталон профильных карт); затем 112-5b этап 2 (W0).

## 2026-09-27 — ревью 112: техдолг п. 4 и 8 закрыт, доработка редактора

По решению пользователя пункты 4 и 8 ревью `docs/review-operator112-2026-09-26.md` сделаны сейчас, а не отложены; из §14 `slice-planning-112.md` убраны. Заодно исправлено замечание собственного `/code-review`: редактор, загрузив форму из устаревшего кэша, больше не требует ручной перезагрузки, если автор ещё ничего не менял.

Changed:
- п. 8: `PUT /scenarios/{id}` требует `base_version_id` (OpenAPI `ScenarioEdit`); и он, и `base_digest` должны совпадать с последней версией, иначе `409 stale_draft` — digest покрывает только тело, а правка одного названия создаёт версию с тем же digest. `approve` на устаревшую версию того же сценария — `stale_draft`, а не 404. Сохранение и утверждение берут `scenarios ... FOR UPDATE` (`Store.ScenarioByIDForUpdate`), параллельные PUT сериализуются. ADR-027 уточнён.
- п. 4: `content.Service.editorIssues` = `ValidateDetailed` + `structuralIssues` во всех точках редактора (создание, PUT, `/validate`, чтение, preview-runs, approve). Тело проецируется в форму файла 112 (без DDS-ключей, `null`, нулевых `dialogue.initial`/`questions`/`recipient_service`, с маркером `schema`) и проверяется `scenario.schema.json` через новый `schema.Validator.FileViolations`; нарушения — `schema_violation`/error, `oneOf`/`anyOf` одной записью. ADR-027 («Без повторной JSON-Schema проверки») дополнен.
- Редактор: форма следует серверной версии, пока нет несохранённых правок (baseline title+body); с правками — только предупреждение.

Validation: `make verify` — ok; `make verify-web`, lint — ok (4 прежних предупреждения); `python3 design-docs/contracts/check.py` — ok; `npm run test:e2e` — 7/7; `make test-integration` — ok в 4 из 5 полных прогонов. Одно падение не воспроизвелось, и его вывод не сохранился; новый тест параллельных сохранений прошёл 15 повторов подряд.

Risks: единичное падение полного интеграционного прогона не объяснено — возможно, ранее существовавший нестабильный тест. Черновики, сохранённые до этого изменения с АОН `"+7"` и т. п., теперь показывают блокирующую ошибку до исправления — это ожидаемо.

Next: п. 7 (эталон профильных карт в редакторе) по решению пользователя; затем 112-5b этап 2 (W0).

## 2026-09-27 — 112-5b этап 2 (разработка): модель в compose, ИИ по умолчанию, `bench-llm`

Решение пользователя 27.09: выполнить этап 2 как задумано — модель отдельным контейнером, `llm` по умолчанию; движок — на выбор разработчика. Выбран `llama-server` ([ADR-029](design-docs/adr/029-operator112-local-inference-container.md)). Коммиты c1–c5 от имени пользователя.

Changed:
- c1: ADR-029; `slice-112-5b-plan.md` — раздел «Этап 2» (коммиты, параметры до замера, шаг на сервере заказчика).
- c2: `llm.Request.Temperature` без `omitempty` — судья с температурой 0 раньше работал с серверной 0.8, а в `assessment_inputs.judge` записывался 0 (замечание ревью). Общая `llm.StripThinking`: закрытый, незакрытый (обрыв по `max_tokens`) и «висячий» `</think>`; её используют `aicaller.CleanReply` и `descjudge.parseAnswers`.
- c3: сервис `llm` в `compose.yaml` (`ghcr.io/ggml-org/llama.cpp:server-b11206` по digest, `./models:ro`, сеть `inference` с `internal: true`, без порта, `--reasoning-budget 0`, `--parallel`/`--ctx-size`/`--threads` из env, `--metrics`, healthcheck `/health` с `start_period` 300 с); worker ждёт `service_healthy` (`required: false`). `CALLER_REPLIER`/`ASSESSMENT_JUDGE` по умолчанию `llm` в compose и в `config.Worker`/`config.API`; без URL/модели — ошибка старта с именем переменной. `CALLER_CONCURRENCY` 3 (+1 судья = 4 слота), `CALLER_REPLY_TIMEOUT` 30 с. `compose.no-llm.yaml` выключает `llm` и возвращает `stub`/`off` (адреса — Ollama хоста); `web/e2e/run.mjs` всегда использует его и явно `stub`/`off`; `make compose-config` проверяет оба варианта. Интеграционные тесты, запускающие процессы `api`/`worker`, явно задают `stub`/`off`. `make model` → `scripts/fetch-model.sh`: реестр Ollama (по умолчанию), `--from-ollama`, `--url`; проверка sha256; `models/` в `.gitignore`/`.dockerignore`.
- c4: `emsim bench-llm` (`cmd/emsim/bench_llm.go`) — виртуальные операторы ведут диалоги по `pilot-112-ai-*` (новейшие версии) через настоящий `aicaller.Replier`, `--judge N` добавляет запросы `descjudge.Handler`; отчёт по уровням: ответы/мин, p50/p95/p99/max, доля дольше `CALLER_REPLY_TIMEOUT`, ошибки, ответы без модели, p95 судьи, токены/с и максимум очереди из `/metrics`; `--json`.
- c5: README (раздел «Модель»), `.env.example`, CLAUDE.md, `slice-planning-112.md`, этот журнал.

Validation: `make verify` — ok; `make compose-config` — ok (оба варианта); `go test -race` стенда — ok; `bench-llm` проверен в собранном образе (`--help`, прогон против недоступного сервера — ошибки в отчёте); `scripts/fetch-model.sh` проверен с поддельным `ollama` (установка, несовпадение pin, повторный запуск, `--url` без sha). `make test-integration` — ok, 443 с; `npm run test:e2e` (через `compose.no-llm.yaml`, образ собран с CA прокси песочницы) — 6/7: все сценарии 112 прошли, упал только скриншот страницы входа в `smoke.spec.ts` (~1% пикселей, сглаживание шрифтов: в песочнице Chromium 1194 вместо 1243, другие шрифты; страница входа не менялась); `npm run lint` — 4 прежних предупреждения.

Not verified (ограничения песочницы): образ llama.cpp не скачан (хранилище GitHub Packages закрыто прокси) — флаги `--reasoning-budget`, `--jinja`, наличие `curl` для healthcheck взяты из Dockerfile/документации llama.cpp, не из запуска; реестр Ollama и Hugging Face закрыты — путь `make model` по умолчанию не запускался, `PINNED_SHA256` пуст. Живой прогон с моделью не выполнялся.

Open issues / risks:
- `PINNED_SHA256` нужно закрепить: `scripts/fetch-model.sh --from-ollama` на машине пользователя печатает sha256 уже проверенных весов.
- Первый `docker compose up` на машине с моделью — проверить, что `llm` становится healthy и судья получает валидный JSON через `response_format` от `llama-server` (`go test -tags=llmlive ./internal/assessment/operator112/descjudge/...` с `JUDGE_LLM_URL` на `llm`).
- Ёмкость класса неизвестна до замера; 20 одновременных генераций 8B на 6 CPU могут не уложиться в `CALLER_REPLY_TIMEOUT`.

Next: на сервере заказчика — `make model`, `docker compose up`, `bench-llm` при `LLM_PARALLEL` 4/8/20, решение по ёмкости; записать в `slice-112-5b-plan.md` и сюда.

## 2026-09-27 — sha256 модели закреплён

`scripts/fetch-model.sh`: `PINNED_SHA256=5ff7c6c3…7a38` — blob `t-tech/T-lite-it-2.1:q5_K_M` из локального Ollama пользователя (получен `--from-ollama`, файл в `models/` проверен). Снимает риск «`PINNED_SHA256` пуст» из предыдущей записи: теперь любой источник (`make model`, `--from-ollama`, `--url`) принимает только эти веса.

Next: первый `docker compose up --build` с моделью у пользователя; затем `bench-llm` на сервере заказчика.

## 2026-09-27 — `llm` в compose перезапускался по кругу: OOM в 8-ГБ Docker VM

Причина: глобальный OOM-killer ядра Docker Desktop VM (7,65 ГБ) убивает `llama-server` при загрузке. При стандартных параметрах RSS процесса — ~7,2 ГБ анонимной памяти (переупаковка весов `CPU_REPACK` ~4,7 ГБ + KV-кеш `--ctx-size 16384` ~2,4 ГБ), запас VM < 100 МБ, поэтому исход зависит от прочей занятости VM (сборка, другие контейнеры); флаги, сеть `internal` и healthcheck ни при чём.

Подтверждено:
- `dmesg` VM: `Out of memory: Killed process … (llama-server) anon-rss:7206940kB`, `global_oom`; `docker events`: `oom` → `die exitCode=137` → `start`; `docker compose ps`: `Restarting (137)`. `ExitCode=0`/`OOMKilled=false` в `docker inspect` — состояние уже новой попытки после рестарта.
- Эксперимент с одной переменной при фиксированной нагрузке 700 МБ (tmpfs-контейнер) и поднятом стеке: стоковый `llm` — 7 рестартов за 48 с; тот же с `--no-repack` (и отдельно через env `LLAMA_ARG_REPACK=false`) — healthy; `LLAMA_ARG_REPACK=true` — снова цикл.
- Но только `--no-repack` при ctx 16384 не годится: 5,4 ГБ весов через mmap не помещаются в страничный кеш, генерация ~0,3 ток/с (11,7 млн major faults). С `LLM_PARALLEL=1 LLM_CTX_SIZE=4096` (контекст на диалог прежний): repack — 5,4 ГБ anon, 6,1–6,8 ток/с; no-repack — 0,65 ГБ anon, 6,4–9,7 ток/с.

Changed: `compose.yaml` (`llm.environment.LLAMA_ARG_REPACK: ${LLM_REPACK:-true}` + комментарий), `.env.example`, README («Модель»: `LLM_REPACK`, раздел «Память» с профилем для 8 ГБ), ADR-029 («Последствия», дополнение о памяти), `slice-112-5b-plan.md` (таблица параметров). Умолчания класса (repack, 4 слота, 16384) не менялись.

Validation: `docker compose up -d --build` с `LLM_REPACK=false LLM_PARALLEL=1 LLM_CTX_SIZE=4096 CALLER_CONCURRENCY=1` — весь стек healthy, `llm` rc=0; `wget` из `worker` на `http://llm:8080/v1/chat/completions` — ответ «голубое»; `make verify`, `make compose-config` — ок.

Risks / not checked: влияние `LLM_REPACK=false` на скорость на целевом x86-64 не замерялось (там умолчание — repack); профиль 8 ГБ проверен только одиночными запросами, не `bench-llm`. Next: W0 `bench-llm` на сервере заказчика (без изменений).

## 2026-09-27 — прогрев кэша промпта ИИ-заявителя (`caller.warmup`)

Решение пользователя 27.09: сделать прогрев сейчас; потоковый показ ответа по словам — только в конце MVP. Живой прогон пользователя на Mac показал ~29 с на первый ответ модели в диалоге (второй вопрос): модель разбирала весь промпт с нуля, последующие ходы брали начало из кэша llama-server.

Changed:
- `aicaller`: `WarmupMessages` (system + история в точности как у следующего `BuildMessages` + пустое user-сообщение; общая часть вынесена в `systemAndHistory`), `Replier.Warm` (`max_tokens: 1`, сценарий без профиля заявителя не прогревается).
- `cmd/emsim/worker_composition.go`: вид задачи `caller.warmup` (пул `caller`, приоритет 50 < 100 у `caller.reply`, 1 попытка). `callerReplyHandler` ставит её в той же транзакции, что применяет вступление (`Source=opening`), dedup — одна на item. Обработчик читает диалог без блокировок, пропускает прогрев, если оператор уже отправил следующий вопрос, вызывает модель вне транзакции и всегда завершает задачу `done` (`result.warmed`/`reason`); item, evidence и журнал не меняются.
- `config.Worker.CallerWarmup` ← `CALLER_WARMUP` (по умолчанию `true`); compose, `.env.example`.
- `bench-llm`: прогрев после вступления параллельно паузе оператора (по `CALLER_WARMUP`, через отдельный неизмеряемый клиент), колонки `first p50/p95` — первый ответ модели в диалоге; `warmup` в отчёте.
- ADR-029 (решение «Прогрев кэша промпта»), README, `slice-112-5b-plan.md`, CLAUDE.md.

Validation: `make verify` — ok; `make compose-config` — ok; `go test -race` стенда — ok; новый интеграционный `TestCallerWarmupPrimesModelAfterOpening` (настоящий процесс worker, `CALLER_REPLIER=llm`, поддельный сервер модели): после вступления ровно один запрос прогрева с `max_tokens=1` и префиксом [system, фраза оператора, вступление, пустой user], первые три сообщения следующего ответа совпадают с ним, одна задача на item — ok. Полный `make test-integration` — см. итог сессии.

Not verified: реальный выигрыш на llama-server (живой модели в песочнице нет) — пользователь сравнит `first p50/p95` в `bench-llm` с `CALLER_WARMUP=false` и без него. Пустое user-сообщение в запросе прогрева проверено только на поддельном сервере.

Risks: при `CALLER_CONCURRENCY=1` (профиль Mac) уже запущенный прогрев занимает пул, и ответ на быстро присланный вопрос ждёт его окончания; в классе с несколькими слотами прогретое начало может вытесниться из кэша до второго хода.

Next: на Mac или сервере — `bench-llm` с прогревом и без; затем W0 на сервере заказчика.

## 2026-09-27 — проверка прогрева кэша промпта (`caller.warmup`) на Mac: кэш работает, логика не менялась

Симптом пользователя: `bench-llm` (1 диалог, 1 мин, пауза 5 с) — `first p50` 22,7 с без прогрева и 38,6 с с прогревом. Проверены гипотезы: (а) прогрев занимает единственный слот дольше паузы; (б) кэш промахивается. Профиль Mac из `.env` не менялся (`LLM_REPACK=false`, `LLM_PARALLEL=1`, `LLM_CTX_SIZE=4096`, `CALLER_CONCURRENCY=1`), `llama-server` `server-b11206`, стандартная verbosity — строк `get_availabl`/`prompt eval time`/`release` хватило.

Кэш работает, (б) отвергнута. Строки `docker compose logs llm` для холодного `llm`:
- прогрев: `selected slot by LRU, t_last = -1` → `prompt eval time = 31246.21 ms / 517 tokens` → `n_tokens = 517`;
- следующий ответ: `selected slot by LCP similarity, f_sim_best = 0.801, f_keep = 0.983` → `prompt eval time = 8066.75 ms / 126 tokens` → `n_tokens = 653`.
`f_keep = 0.983` — из 517 токенов прогрева переиспользовано ~508. Теряется только хвост из ~9 токенов: закрытие пустого user-сообщения, заголовок assistant и сгенерированный токен. Это ~0,5 с при 16 ток/с, поэтому формат запроса прогрева не менялся. Без прогрева первый ответ на холодном сервере разбирает `634 tokens` за 39,5 с.

Причина симптома — (а) и порядок прогонов. Разбор промпта в этом профиле идёт со скоростью ~16 ток/с, поэтому холодный прогрев длится ~31 с. Вопрос, пришедший через 5 с, ждёт в очереди единственного слота. Кроме того, правила роли одинаковы во всех сценариях и остаются в слоте после предыдущего прогона. Поэтому второй прогон (у пользователя — без прогрева) начинается «не с нуля».

Замеры `first p50` (`bench-llm --concurrency 1`), холодный `llm` (`docker compose restart llm` перед каждым прогоном), по одному первому ответу:
- пауза 20 с: 21,86 с с прогревом / 39,34 с без;
- пауза 5 с: 36,09 с с прогревом / 41,27 с без.
Прогоны по 5 мин с паузой 20 с на тёплом слоте — это 2 диалога, 2 первых ответа:
- с прогревом: p50 8,62 / p95 10,72 с (первый ответ разбирает 126 и 141 токен);
- без прогрева: p50 13,06 / p95 15,19 с (199 и 225 токенов).
Обычные ходы — ~150–230 новых токенов, 8–14 с разбора. Это блок фактов и задачи текущей реплики, который по замыслу не кэшируется.

Changed: `README.md` (раздел «Прогрев»: цифры, рекомендация для профиля Mac, сравнение только на холодном `llm`), `LOG.MD`. Код, ADR-029 и тесты не менялись.

Validation: `make verify` — ok; `make compose-config` — ok; `make test-integration` не нужен (Go-код не менялся).

Risks: выборка первых ответов мала — по одному на холодный прогон. Узкое место профиля Mac — разбор промпта (~16 ток/с при `LLM_REPACK=false` в 8-ГБ VM), а не прогрев. На целевом x86-64 с repack скорость другая.

Next: W0 `bench-llm` на сервере заказчика; там сравнивать прогрев тоже на холодном `llm`.
