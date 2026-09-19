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
