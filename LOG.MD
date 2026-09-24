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
