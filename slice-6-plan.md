# Срез 6 — план реализации: оценка по правилам и ручная оценка преподавателя

Контракт: `slice-planning.md` §7, RFC-001 §7.4/§8, ADR-006/013/016, `contracts/{rubric.schema.json, rubric.default.json, assessment-inputs.schema.json, tasks.schema.json, schema.sql, openapi.yaml}`.


## Context

Срезы 1–5 дают закрытую карточку с неизменяемым evidence (`recordDecision`/`closeInterruptedItem` в `internal/training/service.go`), очередь `platform/tasks` (только `pending`; `waiting`/promote/finalizer не реализованы), `lessons.rubric_version` (замораживается при создании из `content.RubricVersion()`), группы `GroupAssessment`/`GroupTraineeSelf` в `internal/auth/authz.go`, пути `/items/{id}/assessment[/revisions]` в OpenAPI и DDL таблиц assessment в `schema.sql` (в миграциях ещё нет). Модуля `internal/assessment` нет.

Цель среза: закрытая training-карточка автоматически получает `auto rev=1` со статусом `needs_review` (детерминированные правила посчитаны, llm-критерии `unavailable`, score/passed NULL) через worker по RFC §7.4; преподаватель через UI видит evidence/журнал/запись, основания каждого нарушения и создаёт экспертные ревизии, которые становятся итоговыми. STT/LLM (срез 9), рекомендации (срез 10), отчёты и ЛК обучаемого (срез 7) не входят.

Решения, согласованные с пользователем: (1) в `rubric.default.json` добавляется deterministic-критерий `D_FIELD_CORRECTIONS` (версия остаётся `dds/rubric-v1`, занятий в БД нет); (2) список карточек для разбора — новый `GET /lessons/{lessonId}/assessments`.

## Правила и семантика (фиксируются в ADR-019 и коде)

### Rubric effective и балл
- `effective = merge(rubric.default, reference.scoring)`: `weights` переопределяют вес, `critical` добавляет критичность, `disabled` исключает критерий. Неизвестный id в scoring уже отклоняется `content.Validate`.
- Нормировка: исключить `disabled` и `not_applicable`, оставшиеся веса нормировать к 100. `unavailable` **не** исключается → итог невозможен → `needs_review`, `score=NULL`, `passed=NULL`.
- Балл критерия: `met=1`, `partial=criterion.score` (0..1, по умолчанию 0.5), `not_met=0`. `score = Σ weight_norm × score_i`.
- Критичность: критерий с `critical=true` (из рубрики или scoring) либо `critical_when=refused_profile_incident` (эталон `accepted`, факт `not_accepted|refused`) в состоянии `not_met` → id в `critical_errors`, `score = min(score, critical_cap)`. `passed = score ≥ pass_threshold && len(critical_errors)==0`.
- `score_override` эксперта заменяет вычисленный балл; `passed` пересчитывается по тем же правилам. `rubric_effective` копируется в каждую ревизию.

### Детерминированные правила ДДС (`internal/assessment/dds`)
Вход: `training.EvidenceBody` (декодируется из `evidence.body`) + `content.Reference` версии сценария (проверка `scenario_digest`) + effective rubric. Каждый результат: `{id, status, score, weight, critical, evidence_refs[], explanation}`; `evidence_refs` вида `action:<uuid>`, `call:<uuid>`, `event:<key>`.

| id | правило |
|---|---|
| `T_OPEN` | `derived.open_seconds ≤ timing.open_s` → met; `≤ partial_until_s` → partial (0.5); иначе not_met. Не открыта: closed → not_met, interrupted → not_applicable |
| `T_PRIMARY` | аналогично по `primary_seconds`/`primary_s` |
| `T_COMPLETE` | `work_seconds` (closed_at − primary_at) vs `complete_s`; без primary: interrupted → not_applicable, иначе not_met |
| `D_PRIMARY` | `derived.primary_status == reference.primary_decision.status`; без решения: interrupted → not_applicable, closed → not_met; `critical_when` см. выше |
| `D_COMMENT_REQUIRED` | `comment_required=false` → not_applicable; иначе met, если есть принятый комментарий (`comments`) |
| `D_FIELD_CORRECTIONS` | (новый) пусто → not_applicable; для каждого `field_corrections[]`: `final_card` по path == expected_value **и** принятый `set_card_field` (по `actions[].effect`) предшествует первому принятому `set_status` со статусом `before_status` → met; все выполнены → met, часть → partial, ни одного → not_met |
| `S_SEQUENCE` | `expected_chain` пуст → not_applicable; `derived.chain` после primary содержит `expected_chain` как упорядоченную подпоследовательность → met, частично → partial, иначе not_met. `events[].expects` в срезе 6 не проверяются (отмечается в ADR) |
| `C_CALL_MADE` | `reference.call.required=false` → not_applicable; завершённый звонок к `call.to` до первого `set_status(before_status)` → met, иначе not_met |
| `C_CALL_LOG` | не требуется → not_applicable; у завершённого требуемого звонка непустые `accepted_by`/`summary` → met; звонка нет → not_met |
| `G_ADDRESS` | тексты `comments` + `call.summary`: адресных упоминаний нет → not_applicable; нормализованные компоненты (street/house/building/apartment) совпадают → met; противоречат → not_met; извлечение неоднозначно → unavailable (ADR-013/016 A4) |
| llm-критерии | `unavailable` (JUDGE=off); `D_COMMENT_CONTENT` → not_applicable при пустом `comment_must_mention`; `C_CALL_CONTENT`/`C_CALL_LOG_CONTENT` → not_applicable при `call.required=false` |

Interruption: маркер `interruptions[]` (server_restart) → все `T_*` not_applicable; `close_reason=interrupted` → не достигнутые этапы not_applicable (см. таблицу), достигнутые считаются по фактам. Отметка `interruption` сохраняется в `feedback[]` ревизии.

### Конвейер задач
- Close (обычный и `lesson.close`) для `mode=training` ставит `assessment.evaluate` в `waiting` (`dedup_key = assessment.evaluate:<item_id>`, scope `item`, payload `{item_id, evidence_digest, rubric_version, input_id:null}`, `wait_until=now`, `wait_reason=awaiting_input`) в той же транзакции, что и evidence. `intro` — ничего.
- Coordinator (цикл worker 2 с, без LISTEN): выбирает `waiting` evaluate c `wait_until ≤ now` без блокировки; каждый — отдельная короткая транзакция: `items FOR UPDATE` → evidence → версия сценария (сверка digest) → правила → `assessment_inputs` (body по `assessment-inputs.schema.json`, `transcripts=[]`, `judge={model:null,...}`, `semantic_input={}`) → `tasks`: payload.input_id + `waiting→pending` атомарно. Ошибка подготовки → `failed/input_preparation_failed` без auto (ручная оценка доступна). Если input уже есть (повтор) — только promote.
- Handler `assessment.evaluate` (pool `llm`, priority 100, lease 5 мин, 3 попытки, retry base 5 с): claim только с `input_id`; `items FOR UPDATE` → если есть expert → выход (Terminal получит `ErrLeaseLost`, т.к. задача уже cancelled); иначе `auto rev=1` из sealed input + `tasks.Terminal(done, {assessment_id,status})` + audit + NOTIFY в одной транзакции. Retry той же задачи не создаёт вторую auto (`assessments_one_auto_idx` + UNIQUE `source_task_id` + проверка под item lock).
- Общий finalizer `Service.FinalizeExhausted`: при последней неудачной попытке (handler: permanent error или retryable на `lease.Attempt==MaxAttempts`) и при истечении lease на последней попытке (reaper) — если input есть и expert нет: `auto rev=1 needs_review` (правила из input, llm unavailable, score NULL) атомарно с `failed`/`dead_letter`; если input нет — только терминальный статус.
- Ручная оценка: `trainee_assessment_state FOR UPDATE` (upsert) → `items FOR UPDATE` → проверка `base_revision` == текущая итоговая (иначе 409 `stale_revision`) → INSERT expert (`revision = base==0 ? 2 : base+1`, `status=ready`, `input_id` = input auto если есть) → `CancelTx` незавершённой evaluate (`waiting|pending|leased`) → `training_examples` для критериев, где expert ≠ auto (только если auto существует) → `trainee_assessment_state.version+1` → audit + NOTIFY. Задачи рекомендаций **не** ставятся.
- Валидация expert: `reason` 3..2000; при `base_revision=0` — полный набор применимых критериев effective rubric (все, кроме disabled); при `base>0` — поправки, остальное копируется из итоговой ревизии; ни одного `unavailable` в результате; неизвестный id / статус → 422 `validation_failed`.
- Порядок блокировок (RFC §8): `trainee_assessment_state → items → tasks`. Finalizer из reaper: domain locks, затем `tasks FOR UPDATE` с повторной проверкой `status='leased' AND lease_token=$token AND lease_expires_at ≤ now-grace`.

### API (обновляется в OpenAPI в C1)
- `GET /api/v1/items/{itemId}/assessment` — только instructor-владелец занятия (`GroupAssessment`); 404 если карточка не закрыта/не его. Ответ: `automatic_state` (статус задачи по dedup_key или `null`, если intro), `final`, `revisions[]`, плюс новые поля `rubric_effective` (для первой ручной оценки без auto) и `evidence` (тело `evidence.schema.json`) — чтобы UI разбора не собирал их из трёх запросов. Чтение обучаемым своего результата — срез 7 (отдельная проекция без эталонных пояснений).
- `POST /api/v1/items/{itemId}/assessment/revisions` — `AssessmentRevision`; 201 `Assessment`; 409 `stale_revision`; 422.
- `GET /api/v1/lessons/{lessonId}/assessments` — (новый) строки закрытых/прерванных карточек занятия: `item_id, user, workstation_no, ordinal, card_number, item_state, close_reason, closed_at, automatic_state, final{revision,kind,status,score,passed}`; instructor-владелец.
- `/admin/tasks/{id}/retry` для evaluate — вне DoD среза 6; фиксируется в LOG как отложенное.

### Хранение
- Миграция `00009_assessment.sql` из `schema.sql`: `trainee_assessment_state`, `assessment_inputs`, `assessments` (+ `assessments_one_auto_idx`, `assessments_item_latest_idx`), `training_examples`, представление `item_final_assessment` (нужно триггеру), триггеры `assessment_inputs_immutable`, `assessments_immutable`, `guard_assessment_revision`. `recommendations`/`advice`/`report_files`/`lesson_report_rows` — в срезах 7/10. `ExpectedSchemaVersion = 9`.
- `assessment_inputs.body` хранится каноническими байтами (как evidence: `content.Canonical`/`Digest`), `digest` от них.

## Структура модуля `internal/assessment`

```
internal/assessment/
  domain.go      Assessment, CriterionResult, CriterionStatus, Kind/Status, ошибки (ErrStaleRevision, ErrValidation, ErrNotClosed…)
  rubric.go      Rubric (типы rubric.schema.json), LoadDefault() из contracts.Files, Merge(default, *content.Scoring) → Effective,
                 Normalize(results) и Score(results, effective) → (score, passed, critical_errors) | needs_review
  input.go       InputBody (assessment-inputs.schema.json), SealInput → (canonical, digest)
  revision.go    правила expert: base_revision, полнота набора, запрет unavailable, копирование из итоговой
  evaluator.go   type RuleEvaluator interface { Evaluate(ev training.EvidenceBody, ref content.Reference, r Effective) []RuleResult }
                 реестр по content.ExerciseType (как training.Exercise)
  dds/rules.go   правила таблицы выше; dds/address.go — нормализация адреса; тесты на фикстурах evidence
  ports.go       ItemReader{ItemByID(…,LockUpdate), RunByID, LessonByID}, EvidenceReader{EvidenceByItem}, ScenarioReader{VersionByID},
                 TaskStore{CancelTx, Terminal, PromoteWaitingTx, FailWaitingTx, DeadLetterExpiredTx, ByDedupKey, WaitingDue},
                 Store (свои таблицы + AuditRecord + WithTx)
  service.go     SealInput, RecordAuto, FinalizeExhausted, CreateExpertRevision, Get, ListForLesson
  coordinator.go tasks.Supervisor: тик 2 с → WaitingDue → SealInput по одному
  postgres/store.go
  http/handlers.go, handlers_test.go
```

Зависимости: `assessment → training` (типы evidence, `KindAssessmentEvaluate`, порты на `*trainingpg.Store` структурно), `assessment → content` (Reference, Canonical/Digest), `assessment → platform/tasks|audit|realtime`. `training` про assessment не знает (только константа kind и enqueue через уже существующий `TaskEnqueuer`).

## План коммитов

### C1 — `docs: define slice 6 assessment contracts`
- Добавить `slice-6-plan.md` (этот план) и ADR-019 «Срез 6: детерминированная оценка, finalizer, экспертные ревизии» (формулы, таблица правил, interruption, G_ADDRESS-эвристика, D_FIELD_CORRECTIONS, отложенный admin retry).
- `contracts/rubric.default.json`: критерий `D_FIELD_CORRECTIONS` (deterministic, `rule: d_field_corrections`, weight 10, `params.applicable_when: reference.field_corrections`); при необходимости `rubric.schema.json` без изменений.
- `contracts/openapi.yaml`: `GET /lessons/{lessonId}/assessments` (+ схема `LessonAssessmentRow`), поля `rubric_effective`/`evidence` в ответе `GET /items/{id}/assessment`, `automatic_state` nullable; проверить `AssessmentRevision`/`CriterionResult`.
- `contracts/tasks.schema.json`: описание `wait_reason=awaiting_input` для evaluate. `contracts/check.py`: положительные/отрицательные проверки рубрики и примера `assessment_inputs` (добавить `assessment-inputs.example.json`).
- Обновить RFC §7.4 только там, где меняется (ссылка на ADR-019).

### C2 — `storage: add assessment schema`
- `migrations/00009_assessment.sql` (см. «Хранение»), `ExpectedSchemaVersion=9`, `test/integration/harness_test.go` (набор таблиц, версия).
- Интеграционные проверки DDL: одна auto на item, `assessments_expert_shape`, `stale assessment revision`, `auto forbidden after expert`, immutability, `input belongs to another item`.

### C3 — `platform: waiting tasks, promotion and exhaustion finalizer`
- `internal/platform/tasks`: `EnqueueRequest.Wait *WaitSpec{Until, Reason}` → INSERT `status='waiting'` (attempts 0, `next_attempt_at` NULL); `WaitingDue(ctx, kind, now, limit)` без блокировки; `PromoteWaitingTx(tx, id, payload, nextAttemptAt)` (`waiting→pending`, `wait_*` NULL); `FailWaitingTx(tx, id, worker, code)` (`waiting→failed`); `ByDedupKey(ctx, tx, key)` (status, payload, id); `DeadLetterExpiredTx(tx, id, worker, token, code)` для finalizer.
- `Recovery.RegisterFinalizer(kind, Finalizer)`; `ReapExpired`: кандидаты читаются как сейчас, но для kind с finalizer и `attempts==max_attempts` строка **не** блокируется в общей транзакции — вызывается `Finalizer.FinalizeExpired(ctx, id, worker, token)` в отдельной транзакции (domain locks → `tasks FOR UPDATE` → повторная проверка → dead_letter). Generic путь без изменений для остальных kind.
- Тесты: `queue_test.go`/`task_queue_test.go` (waiting не claim'ится, promote, fail-waiting, constraint shapes), `task_recovery_test.go` (finalizer вызывается один раз, повтор reaper — no-op).

### C4 — `assessment: rubric, sealing and DDS deterministic rules`
- Пакет `internal/assessment` (domain/rubric/input/revision/evaluator) и `internal/assessment/dds`.
- Unit-тесты: пустой scoring = дефолт, нормировка после disabled/not_applicable, unavailable → needs_review, critical cap, `score_override`; каждое правило таблицы на фикстурах evidence (использовать `training/dds.Exercise.Evidence` для сборки реалистичных снимков, как в `dds/evidence_test.go`); interruption; воспроизводимость (один evidence → одинаковый input digest).

### C5 — `training: enqueue evaluation at close`
- `training.KindAssessmentEvaluate`, `training.EvaluateDedupKey(itemID)`; в `recordDecision` (ветка close) и `closeInterruptedItem` — `EnqueueTx` waiting при `lesson.Mode==training` в той же транзакции, что и `InsertEvidence`. `CloseStoppedLesson` передаёт lesson в `closeInterruptedItem`.
- `Store.EvidenceByItem(ctx, tx, itemID) (Evidence, error)` + реализация.
- Тесты: close → ровно одна waiting-задача с ожидаемым payload; intro → нет задачи; повторное закрытие/lesson.close retry → без дублей (dedup).

### C6 — `assessment: coordinator, auto revision, finalizer and expert revisions`
- `assessment/postgres/store.go`, `service.go`, `coordinator.go`; `cmd/emsim/worker_composition.go`: Spec `assessment.evaluate` (pool `llm`, приоритет 100), handler, регистрация finalizer в `Recovery`, coordinator как Supervisor в composite worker; `cmd/emsim/assessment_composition.go` для api/worker.
- Интеграционные тесты (`test/integration/assessment_*_test.go`): (a) сквозной через реальный `emsim worker`: close → waiting → input → pending → `auto rev=1 needs_review`, score NULL, rule_results совпадают с input; (b) повтор задачи и потеря lease (по образцу `task_recovery_test.go`) → одна auto; (c) исчерпание попыток / истечение lease на последней попытке → finalizer: auto needs_review + failed/dead_letter атомарно; (d) expert без auto (`revision=2, base=0, input_id NULL`) отменяет waiting/pending evaluate, поздний worker не пишет auto; (e) гонка expert vs auto под item lock; (f) `stale_revision`; (g) `trainee_assessment_state.version` растёт на каждую итоговую ревизию; (h) `training_examples` только при существующей auto; (i) ошибка подготовки (повреждённый digest сценария) → failed без auto, ручная оценка доступна.

### C7 — `assessment: instructor HTTP API`
- `internal/assessment/http`: три маршрута, `GroupAssessment`, принадлежность через lesson.instructor_id (как `ItemForInstructor`), коды `stale_revision`/`validation_failed`/`not_found`; регистрация в `cmd/emsim/api.go`.
- Handler-тесты по образцу `training/http/handlers_test.go`: чужой преподаватель → 404, незакрытая карточка → 404, обучаемый → 403, валидация тела, 409.

### C8 — `web: instructor assessment review`
- `web/src/api/assessment.ts` (типы из `schema.d.ts` после `npm run generate:api`), маршруты `/instructor/lessons/:lessonId/assessments` (`LessonAssessments.tsx`) и `/instructor/items/:itemId/review` (`ItemReview.tsx`); ссылки из `LessonDetail.tsx` (stopped/finished) и `Monitor.tsx`.
- Экран разбора: карточка и эталон (существующий `GET /items/{id}` для преподавателя), журнал действий/комментариев/событий из evidence, звонки с прослушиванием (существующий `GET …/recording`), таблица критериев auto с explanation/evidence_refs и явной пометкой «не проверено (unavailable)», история ревизий, форма expert: статус каждого критерия, балл partial, пояснение, `reason`, `score_override`, `base_revision` из загруженного состояния; 409 → уведомление «оценка изменена, обновите». `refetchInterval` пока `automatic_state` не терминальное (SSE-инвалидации item уже приходят в lesson stream — использовать при наличии, иначе polling).
- Минимальный функциональный UI без стилизации АРМ-112.

### C9 — `test: accept slice 6 end to end and document`
- Сквозной e2e через реальные API + worker процессы (по образцу `training_realtime_e2e_test.go`/`lesson_close_worker_e2e_test.go`): занятие → закрытие карточки → auto через worker → список разбора → expert → итог; stop → interrupted → auto с not_applicable timing.
- Ручной happy path в браузере: преподаватель открывает разбор, видит основания, выставляет оценку.
- README (раздел оценки, переменные worker), `LOG.MD` handoff-запись.
- Финальная проверка: `make verify`, `make test-integration`, `make verify-web`, `cd web && npm run lint`, `python3 design-docs/contracts/check.py`, `make compose-config`.

## Верификация
- Юниты: `go test ./internal/assessment/... ./internal/platform/tasks/... ./internal/training/...`.
- Интеграция (Docker): `make test-integration` — новые `assessment_*` тесты плюс существующий набор без регрессий (`lesson.close`, recovery).
- Контракты: `python3 design-docs/contracts/check.py`; `cd web && npm run generate:api && npm run check`.
- Ручная проверка через `docker compose up --build`: seed → занятие с `pilot-tree-02` → обучаемый исправляет округ и принимает → закрывает → преподаватель в `/instructor/lessons/:id/assessments` видит `needs_review`, открывает разбор, `D_FIELD_CORRECTIONS=met`, `D_PRIMARY=met`, llm-критерии unavailable → выставляет expert → итог `ready` с баллом.

## Риски и отложенное
- `G_ADDRESS` — эвристика; при сомнении обязана давать `unavailable`, а не ложный `not_met` (тесты на неоднозначные тексты).
- `events[].expects` не участвуют в `S_SEQUENCE` в срезе 6 — фиксируется в ADR-019 как расширение среза 9/11.
- Admin retry evaluate (`/admin/tasks/{id}/retry`) и чтение результата обучаемым — не в этом срезе (срез 7 / позже); оба отмечаются в LOG.MD.
- Pool `llm` для evaluate уже в срезе 6: `LLM_CONCURRENCY` из compose должен быть ≥1 (проверить `.env.example`/compose).
