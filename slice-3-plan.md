# Срез 3 — план реализации (по коммитам)

Статус: план принят 2026-09-20; реализация по коммитам C0–C8 в процессе (см. `LOG.MD` за актуальным прогрессом).

## Контекст

Срезы 1–2 закрыты (auth, каталог из двух пилотных сценариев `pilot-tree-01/02`, seed, compose). C0 (ниже) коммитит исправления ревью среза 2, которые до этого лежали незакоммиченными в worktree. Срез 3 (slice-planning.md §4) даёт первый сквозной прогон: преподаватель создаёт занятие → назначает одну карточку одному обучаемому на одном РМ → старт → обучаемый открывает, действует, закрывает → журнал + неизменяемый evidence. Worker/SSE/события/stop/телефон — следующие срезы.

План пользователя (согласован с другой LLM) принят как основа. Проверен против ADR-004/011/015/016, RFC §6–7, `contracts/schema.sql`, `openapi.yaml`, `evidence.schema.json`, seed-сценариев и текущего кода. Критических расхождений нет; ниже он уточнён деталями реализации и двумя корректировками (см. «Уточнения»).

### Согласованные пилотные правила (без изменений)
- `pilot_goal=accept_card` → `close` разрешён после `accepted`; `reaction` остаётся `accepted`, `close_reason=pilot_completed`. Не означает правильного выполнения.
- `set_card_field` только для `/card/address/okrug`, непустая строка, после `open` и до `accepted` (reaction ∈ {received, not_accepted}). Значение не проверяется; эталон скрыт.
- `intro` и `training` принимаются; оба сохраняют evidence, без задач оценки/уровня.
- Пилотный workflow (`seed/services.json`): `added→received→{accepted|not_accepted}`, `not_accepted→accepted`, `comment_required=[not_accepted]`, `terminal=[]`. Значит доступны два завершения: `accepted→close(pilot_completed)` и `not_accepted(+comment)→close(refused)` по ADR-011.

### Уточнения к плану пользователя
1. **Результат изменения поля** хранить в новой колонке `actions.effect jsonb` (`{path, old, new}` — серверный факт), а не в `payload` (клиентское тело, участвует в `request_digest`). Evidence копирует `effect`.
2. **Playwright** (C8) — отложен по решению пользователя: DoD среза закрывает Go-тест против реального `api`-процесса (harness `test/integration/api_process_test.go`) + ручной UI-прогон. Playwright — отдельная задача после среза 3.
3. **РМ обучаемого**: `/my/run` отдаёт прогон всегда (с `workstation_no`), чтобы UI мог показать «занятие назначено на РМ-N»; `/my/items`, `/items/{id}`, `POST actions` при `session.workstation_id ≠ run.workstation_id` → `403 forbidden` (details `{reason: workstation_mismatch}`).
4. Коммиты: без `Co-Authored-By`/упоминания Claude (CLAUDE.md).
5. **Кросс-модульные чтения в одной транзакции**: порты training принимают `pgx.Tx` и реализуются адаптерами в `cmd/emsim` поверх `authpg.Store`/`contentpg.Store` (их методы уже принимают `tx`), а не поверх `*auth.Service`/`*content.Service` (те открывают своё соединение → deadlock при `pool_max_conns=1`, см. LOG 2026-09-20). В `content` добавить только `Store.VersionByID` (полная запись версии по id — сейчас есть лишь `VersionReferenceByID`) и экспортированные `Workflow.Allows(from,to)`/`RequiresComment(r)`.
6. `GET /items/{itemId}` по OpenAPI доступен trainee (без reference) и instructor (с reference); в `authz.go` нет группы на обе роли → добавить `GroupItemRead {instructor, trainee}`, ветвление и проверка принадлежности в handler.
7. `CardView` = поля `CardPreview` (`internal/content/preview.go`), но `registered_at` абсолютное (`offered_at + registered_at_offset_s`); `IncidentCard` расширить пропсом для абсолютного времени, не дублировать компонент.

---

## C0 — `fix: finalize slice 2 review corrections`
Закоммитить явно перечисленные файлы из `git status` (19 изменённых + `migrations/00005_...`). `c.txt` не добавлять. Проверка уже выполнена (LOG.MD 2026-09-20T09:33) — повторить только `make verify`.

## C1 — `docs: define slice 3 pilot rules and contracts`
- Новый `design-docs/adr/017-slice3-pilot-close-and-field-correction.md`: правила выше; `pilot_completed`; `set_card_field`; `Assignment.user_id` обязателен; intro/training; что отложено.
- `design-docs/rfc-001-emsim.md` §7.4 (close: пилотное исключение), §6 (`set_card_field`, `effect`); `slice-planning.md` §4 — ссылка на ADR-017 и `pilot_completed`. Новый `slice-3-plan.md` = этот план.
- `contracts/openapi.yaml`: `Assignment.user_id` required (non-nullable); `Command.type` + `set_card_field` с payload `{path: const "/card/address/okrug", value: string minLength 1}`; `Action.effect`; `ItemSummary.close_reason` + `Item.close_reason` с `pilot_completed`; `Item.mode`, `Item.allowed_transitions`; `CardView` — полная публичная проекция (те же поля, что `CardPreview`, но `registered_at` абсолютное = `offered_at + registered_at_offset_s`); `MyRun.workstation_no`; `GET /lessons/options` → `{trainees:[{id, full_name, service_code, service_name}], workstations:[{id, no, name}]}` (только active, только trainee; instructor-роль, `GroupLessons`); `Lesson.assignments[]` с `user_id`; описание, что stop/monitor/stream/calls — не реализованы в срезе 3.
- `contracts/schema.sql`: синхронно с миграцией 00006 (ниже).
- `contracts/evidence.schema.json`: `close_reason` + `pilot_completed`; `mode` (required); `final_card` (объект, required); `actions[].effect`; `evidence.example.json` обновить. `check.py`: валидировать пример evidence, добавить позитивные/негативные примеры `Command` (`set_card_field` с чужим path → fail) через jsonschema по извлечённым компонентам OpenAPI.
- `cd web && npm run generate:api`.
- Seed-файлы не менять.

## C2 — `training: add lesson run item and evidence storage`
`migrations/00006_training_lessons_runs_items.sql` (goose, стиль 00004/00005):
- `lessons` как в schema.sql (`exercise_type` CHECK dds_processing; `timing`, `rubric_version`, `epoch`, `recording_grace_s`, state-shape CHECK).
- `assignments`: `user_id uuid NOT NULL`, PK `(lesson_id, workstation_id)`, `UNIQUE (lesson_id, user_id)`, `scenario_version_ids uuid[]`.
- `runs`: + `mode text NOT NULL CHECK (mode IN ('intro','training'))`; частичные уникальные индексы `runs_active_user_idx ON runs(user_id) WHERE state='active'`, `runs_active_workstation_idx ON runs(workstation_id) WHERE state='active'` → однозначный `/my/run`.
- `items`: как в schema.sql + `card jsonb NOT NULL` (экземпляр публичной карточки, изменяемый `set_card_field`), `workflow jsonb NOT NULL` (снимок `services.workflow` на старте), `pilot_goal text` (снимок из reference; ''/NULL = обычные правила), `close_reason` CHECK + `pilot_completed`.
- `actions`: как в schema.sql + `effect jsonb NOT NULL DEFAULT '{}'`, `type` CHECK + `set_card_field`; `actions_immutable` trigger (функция `reject_immutable_change()` — создать в этой миграции, если её ещё нет).
- `evidence`: как в schema.sql + `evidence_immutable` trigger.
- Не создавать: `item_events`, `calls`, `control_reports`, assessment-таблицы.
- `internal/platform/postgres/postgres.go`: `ExpectedSchemaVersion = 6`; `applicationTables` + `lessons, assignments, runs, items, actions, evidence` (иначе `Ready()` не пройдёт).
- `test/integration/harness_test.go::resetSchema`: DROP `evidence, actions, items, runs, assignments, lessons` + `reject_immutable_change()` перед content-таблицами; `assertTableSet`/`assertApplicationTablesAbsent` обновить; `TestPlatformSchema` версия 6.
- Интеграционный тест: миграция с пустой БД и поверх 5; UPDATE/DELETE actions/evidence → ошибка; второй active run того же user → violation; users/каталог сохраняются.

## C3 — `training: implement DDS rules and evidence projection`
Новый пакет `internal/training` (домен, без HTTP/SQL) и `internal/training/dds`:
- `training/domain.go`: типы `Lesson`, `Assignment`, `Run`, `Item` (state, reaction, seq, log_seq, card, workflow, deadlines, timing, pilot_goal, primary_at…), `Action`, `Command{CommandID, ExpectedSeq, Type, Payload, ClientAt}`, `Receipt`, `Rejection` (коды из `httpapi.ErrorCode`: stale_seq/item_closed/lesson_stopped/transition_not_allowed/comment_required/invalid_payload), `RequestDigest(actorID, itemID, type, payload, expectedSeq, clientAt)` = sha256 канонического JSON (переиспользовать `content.Canonical`).
- Интерфейс процесса упражнения (`training/exercise.go`):
  `type Exercise interface { Decide(item Item, cmd Command, now time.Time) (Decision, error) ; Evidence(...) }` — `Decision{Accepted, Rejection, NewReaction, NewState, Effect, SetPrimaryAt, Close *CloseReason}`. Реализация выбирается по `exercise_type` (только `dds`).
- `training/dds/rules.go`: `open` (offered→opened, reaction added→received), `set_status` (по `item.workflow.transitions`; `comment_required` → нужен `payload.comment`, иначе `comment_required`; первое applied решение из `received` фиксирует `primary_at`, `deadlines.complete_at = primary_at + complete_s`; повтор того же статуса → `transition_not_allowed`), `add_comment` (в open-состоянии), `set_card_field` (path allowlist, непустое значение, reaction ∈ {received,not_accepted}; `effect{path,old,new}`; пересобрать `card.address.text` по той же схеме, что в seed `«Россия, Москва, (ЮАО, Чертаново Южное), …»` — единая функция форматирования), `close` (ADR-011: после not_accepted/refused → `refused`; completed/completed_without_team → `completed`; при `pilot_goal=accept_card` и reaction=accepted → `pilot_completed`; иначе `transition_not_allowed`). Превышение сроков не закрывает. `item_state` после первого applied `set_status` → `in_progress`.
- `training/dds/evidence.go`: сборка `Evidence v1` по `evidence.schema.json`: `mode`, `final_card`, `actions` до `cutoff_log_seq` с `effect`, `events=[]`, `calls=[]`, `comments`, `derived{open_seconds, primary_seconds, work_seconds, total_seconds, primary_status, chain, rejected_transitions, comment_count, call_count=0}`, `interruptions=[]`, `interruption=null`. Digest = sha256(`content.Canonical(body)`). Валидировать в тесте через `internal/content/schema`-подобный валидатор (расширить `schema.Validator` на `evidence.schema.json` из `contracts.Files`).
- Тесты: оба пилота полностью; отказ set_card_field после accepted; close из received; not_accepted без комментария; повтор статуса; таймеры; close без pilot_goal из accepted → отказ.

## C4 — `training: implement transactional lessons and commands`
- `internal/training/service.go` — прикладные операции:
  - `CreateLesson(instructor, LessonCreate)` (defaults timing 30/30/180, `rubric_version` из `rubric.default.json` — вынести версию в `content` экспорт `RubricVersion()`; `spawn_every_s` → отклонять как unsupported).
  - `ReplaceAssignments(instructor, lessonID, []Assignment)` — только owner, только draft; ровно одно назначение с одной версией (в срезе 3); проверки через порты: `UserPort.TraineeByID` (active, role trainee, service_code), `WorkstationPort.ByID/ByNo` (active), `ScenarioPort.VersionForTraining(id)` → `{status approved, exercise_type, target_service, digest, body}`; сервба обучаемого = `target_service`; версии с events/`reference.call.required` → 422 unsupported.
  - `Start(instructor, lessonID)` — `lessons FOR UPDATE`; draft→running, `started_at=clock_timestamp()`; повторные проверки; создать `runs` (level_at_start, mode, exercise_type) и `items` ordinal 1 (card = `content.ProjectCard`-подобный экземпляр с абсолютным `registered_at`, workflow snapshot из `services`, `timing_effective`, `deadlines{open_at, primary_at}`, `pilot_goal`); повтор start на running → 200 тот же Lesson без дублей (идемпотентно), на finished → 409.
  - `Execute(actor Principal, itemID, Command)` — RFC §7.1: авторизация/принадлежность (item→run.user_id=actor, run.workstation_id=session.workstation) **до** транзакции; в tx: `lessons FOR SHARE` (для `close` — сразу `FOR UPDATE`, без апгрейда) → `runs FOR UPDATE` (только close) → `items FOR UPDATE` → поиск `actions.command_id` (replay: сравнить actor/item/digest → исходный receipt+http_status+`replayed=true`; иначе `command_id_conflict`) → проверки running/closed/expected_seq → `Exercise.Decide` → `clock_timestamp()` после блокировок → INSERT actions (log_seq+1, receipt, http_status, effect) → при applied: UPDATE items (seq+1, state, reaction, card, deadlines, primary_at) → при close: evidence INSERT, item closed, run finished (очередь исчерпана), lesson finished → `audit.Record` → COMMIT. Отказы (stale_seq, transition_not_allowed…) тоже коммитятся в actions, seq не растёт. `invalid_payload` структурно некорректного тела — не авторизованная попытка → не в журнал (Error 400/422 до транзакции).
  - Чтение: `MyRun(user)`, `MyItems(user)`, `ItemForTrainee(user, itemID)` (без reference), `ItemForInstructor`, `RunActions(instructor, lessonID, runID)`, `ListLessons/Lesson`, `LessonOptions`.
- Порты объявляются в `training/service.go` (consumer-owned), принимают `pgx.Tx`: `UserDirectory.UserByID(ctx, tx, id)`, `WorkstationDirectory.ByID/ByNumber(ctx, tx, …)`, `ScenarioReader.VersionByID(ctx, tx, id) → {Status, ExerciseType, TargetService, Digest, Body, BodyJSON}`, `ServiceReader.ByCode(ctx, tx, code) → {Workflow}`. Реализация — адаптеры в `cmd/emsim/training_ports.go` поверх `authpg.Store` и `contentpg.Store` (методы `UserByID`, `WorkstationByID`, `ServiceByCode`, новый `VersionByID`), без импорта training из auth/content.
- Rubric version: экспортировать из `content/rubric.go` `RubricVersion() (string, error)` (читает `version` из embedded `rubric.default.json`).
- Digest evidence: сериализовать body → декодировать с `UseNumber()` в `map[string]any` → `content.Digest` (требование `Canonical` к json.Number-дереву).
- `internal/training/postgres/store.go`: `WithTx`, CRUD выше; `pgx.Tx` передаётся явно (стиль `content/postgres`).
- `audit.Record` в той же транзакции (lesson.create/assign/start, item.command applied/rejected, item.close).
- Тесты (integration): конкурентные start; конкурентные команды одного item (один applied, второй stale_seq); replay applied и rejected; replay после close; другой body под тем же command_id → 409; откат при ошибке evidence (инъекция через сломанный digest/константу); два прохождения одной версии независимы.

## C5 — `training: expose lesson and trainee HTTP API`
`internal/training/http/handlers.go`, `Register(mux)` в стиле `content/http` (`SessionMiddleware` + `RequireRole(auth.GroupLessons|GroupTrainee)`):
- Instructor: `GET/POST /lessons`, `GET /lessons/options` (регистрировать литеральный путь — Go 1.22 mux даёт ему приоритет над `{lessonId}`), `GET /lessons/{id}`, `PUT /lessons/{id}/assignments`, `POST /lessons/{id}/start`, `GET /lessons/{id}/runs/{runId}/actions`. Только owner; admin → 403 (группа), чужой instructor → 404.
- Trainee: `GET /my/run` (204 без active run), `GET /my/items`, `POST /items/{id}/actions` (`GroupTrainee`); `GET /items/{id}` под новой `GroupItemRead` (trainee-owner → без reference, закрытый item доступен; instructor-owner занятия → с `reference`; иначе 404).
- DTO обучаемого — отдельные структуры без `reference`, `field_corrections`, `pilot_goal`, `hints`. Тест «точной формы» ответа (ключи allowlist) как в content.
- Командный endpoint: 200/409/422 с `Receipt` (`error_code` в теле); `command_id_conflict`/403/404/400 — `Error`. `DecodeJSON` лимит 64 КБ.
- `cmd/emsim/api.go`: `training.NewService(trainingpg.NewStore(pool), adapters…)`, `traininghttp.NewHandlers(...).Register(apiMux)`.
- Тесты handlers с fake service: роли, принадлежность, wrong workstation → 403, утечка эталона, коды.

## C6 — `web: add instructor lesson creation and start`
- `web/src/api/training.ts`: `useLessons`, `useLesson`, `useLessonOptions`, `createLesson`, `replaceAssignments`, `startLesson`.
- `web/src/routes/instructor/Lessons.tsx` (список + создать: title, mode=training, level=easy, timing 30/30/180), `LessonDetail.tsx` (выбор обучаемого/РМ/сценария из `useLessonOptions` + `useScenarios({status: approved})`, сохранить назначение, «Запустить», после старта — состояние, назначение, `finished`). Ошибки 422/409 показывать через `api/errors.ts`.
- `App.tsx` маршруты `/instructor/lessons`, `/instructor/lessons/:lessonId`; `Layout.tsx` ссылка; `Home.tsx`.

## C7 — `web: add trainee card and durable command recovery`
- `web/src/api/commands.ts`: транспорт, понимающий `Receipt` на 200/409/422 (не через `ApiError`), `Error` на остальных.
- `web/src/commands/pending.ts`: `localStorage` ключ `emsim.pending.<user_id>.<item_id>` → неизменяемое тело команды; одна незавершённая на item; при старте экрана — replay сохранённой команды; удаление по квитанции; при `stale_seq` перечитать item, новое намерение = новый `command_id` (`crypto.randomUUID()`); при недоступном localStorage — предупреждение.
- `web/src/routes/trainee/Workplace.tsx`: `useMyRun`/`useMyItems`/`useItem` с `refetchInterval: 2000` + `refetchOnWindowFocus`; список карточек; карточка через `IncidentCard` (проп абсолютного `registered_at`); кнопки «Открыть», «Принять», «Не принять» (+ комментарий), «Комментарий», редактирование округа (до принятия), «Завершить упражнение». Сервер добавляет в `Item` поле `allowed_transitions: ReactionStatus[]` (из workflow-снимка item) — UI показывает кнопки по нему, отказы (`transition_not_allowed`, `comment_required`) отображает из `Receipt`. Таймеры от `deadlines`/`server_time`. После закрытия — «Упражнение завершено», без балла/утверждения об успехе. Предупреждение при несовпадении РМ.
- `Home.tsx` для trainee → редирект на `/my`.

## C8 — `test: verify slice 3 end to end and document acceptance`
- `test/integration/training_e2e_test.go` против реального `api`-процесса (по образцу `TestAPIProcessContentCatalogAccess`): оба пилота (01: open→accepted→close; 02: open→set_card_field→accepted→close; и 02 без исправления — закрытие разрешено, evidence хранит `ЮАР`); два прохождения одной версии; импорт новой версии не меняет `items.scenario_version_id`/card; конкурентные команды; replay после «потерянного ответа»; `intro` без задач в `tasks`, `users.level` не меняется; отсутствие `reference` в trainee-ответах (grep ключей); завершённый run освобождает user/РМ для следующего занятия.
- Playwright не добавляется (решение пользователя); восстановление из `localStorage` проверяется вручную (шаг 3 верификации) и unit-тестом чистой логики `pending.ts` при наличии тест-раннера — иначе только вручную, зафиксировать в LOG.MD.
- `README.md` (demo walkthrough: занятие → карточка), `LOG.MD` handoff-запись.
- Прогон: `make verify`, `make test-integration`, `make verify-web`, `cd web && npm run lint`, `python3 design-docs/contracts/check.py` (+ `DATABASE_URL` на временной БД), `make compose-config`, `docker compose up --build` + ручной UI-прогон обоих пилотов (преподаватель + обучаемый в разных профилях браузера).

## Верификация (сквозная)
1. Чистая БД: `migrate` → `bootstrap-admin` → `import seed` → создать instructor/trainee(service=dds_district)/РМ через admin UI.
2. Instructor UI: занятие → назначить pilot-tree-02 → старт.
3. Trainee UI (другой профиль, РМ выбран): карточка видна ≤2 с; открыть; исправить округ ЮАР→ЮАО; принять; F5 — состояние сохранено; отключить сеть, нажать «Принять» повторно/«Завершить» → после восстановления replay без второго effect; «Завершить упражнение» → закрыто, `close_reason=pilot_completed`.
4. SQL: `SELECT * FROM evidence` — один снимок, digest = sha256(canonical body); `UPDATE evidence` → ошибка; `actions` содержит applied + rejected попытки с `log_seq` без пропусков.
5. Повторить пилот 01 вторым обучаемым на той же версии → независимые items.
