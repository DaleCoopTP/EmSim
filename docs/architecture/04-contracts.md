# Контракты кода, API и событий

Все контракты ниже — целевая версия `emsim/v1`; существующий wire protocol core не объявляется совместимым. [types.go](contracts/types.go) — компилируемый эскиз основных портов, а не готовый SDK или реализация. Полный OpenAPI/JSON Schema создаётся из этой спецификации в W1/W2 и проверяется контрактными fixtures.

## 1. Командный протокол

Каждая изменяющая команда имеет `command_id` UUID; idempotency scope — `(actor_id, operation, resource_id, command_id)`. Canonical payload hash включает path, operation, ожидаемую версию и тело, исключает изменяемые transport headers. Для создания resource ID scope — parent/collection ID. Одинаковый ключ и тело возвращают сохранённый receipt; другое тело — `409 idempotency_conflict`. Повтор receipt проходит актуальную авторизацию: угаданный UUID не даёт чужих данных.

Пример команды задачи 2:

```json
{
  "schema": "emsim/command/v1",
  "command_id": "11111111-1111-4111-8111-111111111111",
  "expected_version": 17,
  "interaction_epoch": 3,
  "client_seq": 8,
  "client_occurred_at": "2026-09-16T13:00:02Z",
  "item_id": "22222222-2222-4222-8222-222222222222",
  "type": "reaction.record",
  "payload": {
    "service_id": "dds-training-01",
    "status": "not_accepted",
    "comment": "Объект вне зоны обслуживания. Информация передана профильной службе."
  }
}
```

Ответ после commit:

```json
{
  "command_id": "11111111-1111-4111-8111-111111111111",
  "outcome": "applied",
  "run_version": 18,
  "last_event_seq": 31,
  "server_time": "2026-09-16T13:00:02.180Z",
  "replayed": false
}
```

`outcome` различает `applied`, `rejected`, `recovered_only`. `rejected` может иметь durable receipt/audit, не меняя domain state. Команда с конфликтом версии не заменяет серверные данные; клиент перечитывает snapshot и после явного разрешения конфликта отправляет **новый** command ID. Для draft text сохраняется полный field value или patch с base version; типовые статусные команды всегда explicit intent, не JSON overwrite всего run.

Ошибки имеют `code`, `request_id`, безопасный `message`, при необходимости `current_version` и `retry_after_ms`. HTTP: `401` нет сессии; `403` известный разрешённый контекст без permission; `404` чужой/неизвестный ресурс без раскрытия существования; `409` version/idempotency/terminal conflict; `422` структурная ошибка/недопустимый переход; `429` admission; `503` недоступна обязательная инфраструктура. Для stopped run replay intake возвращает receipt `recovered_only` со ссылкой на supplemental input, если есть новые неподтверждённые данные.

## 2. API inventory

Общий prefix `/api/v1`; schema-validated JSON, pagination cursor+limit, bounded payloads, request IDs. `C` — идемпотентная команда, `Q` — запрос, `J` — асинхронная операция с `operation_id`/Location. Actor из authenticated session, не из тела. Для `PATCH` разрешены только типизированные DTO, не произвольные JSON пути.

| Методы и путь | Тип | Пользователь / результат |
|---|---|---|
| `POST /auth/login`, `/auth/logout`; `GET /auth/session`; `/auth/oidc/*` | C/Q | Вход/выход/сессия, локальный OIDC flow |
| `GET/POST /users`; `PATCH /users/{id}`; `POST /users/{id}/block`, `/unblock`, `/permissions` | C/Q | Admin IAM permission; create/change/block/roles |
| `GET/POST /groups`; `POST /groups/{id}/members` | C/Q | Instructor своей группы; IAM roles не меняются |
| `GET/POST /scenarios`; `GET/PATCH /scenarios/{id}/draft` | C/Q | Instructor author/authorized collaborator |
| `POST /scenario-generations`; `POST /scenarios/{id}/regenerate` | J | Генерация draft и коррекция по комментарию |
| `POST /scenarios/{id}/validate`, `/grammar-check`, `/publish`, `/archive` | C/J | Validation report, immutable published version |
| `GET /scenarios/{id}/versions/{version}` | Q | Role-specific view; operator только разрешённая публичная часть |
| `GET/POST /rubrics`; `POST /rubrics/{id}/publish` | C/Q | Versioned timing/criteria/weights/thresholds |
| `POST /materials`; `GET /materials/{id}` | C/Q | Upload/retrieve scoped local PDF/DOCX/XML/JSON |
| `POST /classifier-imports`; `GET /classifier-imports/{id}`; `POST /classifier-imports/{id}/publish` | J/C/Q | Staging→review→version; importer permission |
| `GET /classifiers/{version}/types`, `/routing-rules`; `GET /services` | Q | Выбор признаков/служб, роль и public fields |
| `GET/POST /card-templates`; `POST /card-templates/from-item`; `POST /card-templates/{id}/approve`, `/archive` | C/Q | Source provenance, redaction, approved version |
| `GET/POST /lessons`; `GET/PATCH /lessons/{id}` | C/Q | Instructor owner; edit only draft |
| `POST /lessons/{id}/assignments`; `POST /lessons/{id}/validate`, `/start`, `/finish`, `/stop` | C | Группы, список sources, тайминги, start/stop barrier |
| `GET /lessons/{id}/monitor` | Q | Компактная live view; per-run состояния и last_updated |
| `GET /assignments`; `POST /assignments/{id}/runs` | Q/C | Operator own assignment; admission and new attempt |
| `GET /runs/{id}`; `GET /runs/{id}/snapshot` | Q | Authorised view, version, seq, active item, connectivity |
| `POST /runs/{id}/commands` | C | Allowlisted message/card/reaction/comment/dispatch/contact-control commands |
| `POST /runs/{id}/items/{item}/submit`; `POST /runs/{id}/next-card`; `POST /runs/{id}/finish` | C | Actor owns run; lesson/assignment policy checked |
| `POST /runs/{id}/stop` | C | Instructor of lesson; individual barrier, reason required |
| `GET /runs/{id}/events?after={seq}` | Q/SSE | Authorised durable event stream; Last-Event-ID support |
| `POST /runs/{id}/reconnect` | C | Client receipt frontier, unacked IDs, snapshot + replay disposition |
| `POST /runs/{id}/recovered-inputs` | C | Late/offline text/action capture; supplemental only if sealed |
| `POST /runs/{id}/media-sessions`; `POST /media-sessions/{id}/close` | C | Short-lived media credentials, binding to device/run/item |
| `POST /media-sessions/{id}/chunks`; `GET /media-sessions/{id}/manifest` | C/Q | Bounded upload, chunk ID/hash dedup; recover stored audio |
| `GET /runs/{id}/evidence`; `GET /runs/{id}/assessments` | Q | Role-filtered evidence/results; hidden rubric not leaked during run |
| `POST /assessments/{id}/review`, `/reassess`, `/publish` | C/J | Instructor, reason + new revision; old result preserved |
| `GET /operators/{id}/progress`; `GET /groups/{id}/progress` | Q | Own user / own teaching group |
| `POST /reports`; `GET /reports/{id}`; `GET /reports/{id}/download` | J/Q | CSV/PDF, bounded scope/period, ready/provisional status |
| `POST /group-insights`; `GET /group-insights/{id}` | J/Q | Фоновая AI-рекомендация, versioned input aggregate |
| `GET /operations/health`, `/metrics-summary`, `/logs`, `/usage` | Q | Technical admin, sanitized; no student text by default |
| `POST /operations/backups`, `/restore-checks`, `/updates`, `/service-actions`; `GET /operations/{id}` | J/Q | Allowlist ops, step-up, scope, mandatory audit |
| `GET/PATCH /configuration`; `GET /audit-events` | C/Q | Separate config/security/audit permissions; version/diff |
| `GET /operations/{id}/result` | Q | Результат любой async operation внутри разрешённого scope |

Во всех J endpoints API возвращает persisted operation state; отсутствие worker не теряет запрос. Operation lifecycle `queued → running → ready|failed|cancelled`, ошибки доступны с безопасным кодом. Отчёты и материалы скачиваются через ACL gateway/короткую внутреннюю signed link; bearer URL не вечная замена авторизации.

## 3. Внутренние application services и ports

| Application service | Зависит от портов | Транзакционный результат |
|---|---|---|
| `PublishScenario` | ScenarioRepository, Validator, AuditWriter | Version + snapshot digest + published event |
| `StartLesson` | LessonStore, SourcePoolResolver, AssignmentStore, AuditWriter | Frozen plan/pool + status/epoch + event |
| `StartRun` | TrainingTransaction, AssignmentPolicy, SnapshotReader | Run/item + manifest + initial task + receipt |
| `ApplyCommand` | Authorizer, TrainingTransaction, DomainPolicy | State/evidence/event/audit/task + receipt |
| `GenerateTurn` | TurnLoader, CallerSimulator, TurnCommitter | One message + disclosure state + task done + event |
| `OfferNextCard` | TrainingTransaction, CardSelector | Selected source + new item + timer + receipt |
| `StopLesson` | TeachingTransaction, AuditWriter, CompletionScheduler | Global barrier + durable close job |
| `SealRun` | EvidenceBuilder, SealCommitter | Immutable evidence + terminal run + assessment request |
| `EvaluateEvidence` | EvidenceReader, DeterministicEvaluator, SemanticJudge | Criteria results, evaluation attempt, evaluation task state |
| `FinalizeAssessment` | ScoreAggregator, AssessmentCommitter | Immutable result revision + event + projection update |
| `ReviewAssessment` | AssessmentPolicy, AssessmentCommitter | Expert revision + audit + versioned reporting event |
| `BuildReport` | ScopedProjectionReader, Exporter, BlobStore | Artifact manifest + ready operation |

Clock, IDSource, hash/canonicalization, typed failure taxonomy — узкие infrastructure ports. Не создавать универсальные интерфейсы «на всё» ради DDD. Репозитории не возвращают `pgx.Rows` или HTTP DTO.

## 4. Published language

### `PublishedScenarioV1`

Identity/version; mode; difficulty; categories; public brief; private facts с fact IDs; initial disclosure; disclosure rules; expected card/allowed semantic variants; expected action constraints; rubric ref+snapshot; classifier/workflow versions; timing policy; approved material refs; model/prompt manifests; approved_by/at; canonical digest. Secrets/endpoints не входят. Размеры/turn/token limits задаются явно, а не наследуются от synthetic core (его max 32 пары не является требованием продукта).

### `RunEvidenceV1`

`run_id`, `operator_id`, `assignment_id`, `lesson_id`, `mode`, `snapshot_digest`, `cutoff_seq`, `items[]`, `created_at`, `stop_reason`, `coverage`, `digest`.

Item evidence: `item_snapshot_digest`, source/provenance refs; source snapshot; frozen item-specific reference/rubric; messages `{id, speaker, text, server_at, utterance_id?, audio_refs?, delivered_range?}`; card field values/versions; actions `{id, type, payload, accepted, rejection_reason?, server_at, client_at?}`; timers/norms; service reactions; technical interruptions; final card; partial/complete marker. Крупные аудио — refs+hashes, не JSON/base64 в DB.

Canonical serialization задана версией: нормализованные timestamps UTC, стабильный порядок объектов/массивов с явно заданной семантикой, сохранение исходного пользовательского текста. Digest считается по canonical bytes, не по произвольному `jsonb::text`. Evidence сохраняется в PostgreSQL как bounded JSON manifest + нормализованные факты; heavy binary refs указывают immutable blobs. Snapshot operator ID доступен только согласно ACL.

### `AssessmentResultV1`

`assessment_id`, `revision`, `evidence_digest`, `rubric_digest`, `evaluator_manifest`, `status`, `items[]`, `criteria[]`, `score?`, `verdict?`, `coverage`, `feedback`, `created_at`, `published_at?`, `supersedes_revision?`. `score`/`verdict` отсутствуют при unavailable, а не равны нулю. `Feedback` содержит criterion ID, severity, evidence IDs/field ranges, explanation, recommendation. Judgment rationale — короткое проверяемое обоснование, не требование к раскрытию chain-of-thought модели.

## 5. События и очереди

Envelope:

```json
{
  "event_id": "33333333-3333-4333-8333-333333333333",
  "type": "training.run_sealed.v1",
  "aggregate_id": "44444444-4444-4444-8444-444444444444",
  "aggregate_version": 29,
  "occurred_at": "2026-09-16T13:05:00Z",
  "correlation_id": "request-or-process-id",
  "causation_id": "command-or-event-id",
  "scope_id": "lesson-scope-id",
  "payload": {"evidence_id": "evidence-id", "evidence_digest": "sha256:..."}
}
```

| Событие | Producer → consumer | Effect и dedup |
|---|---|---|
| `curriculum.scenario_published.v1` | curriculum → teaching/catalog | Обновить доступный каталог по version ID |
| `catalog.card_approved.v1` | catalog → teaching | Доступность для новых frozen pools; текущий pool не меняется |
| `teaching.lesson_started.v1` | teaching → reporting | Создать monitor rows, идемпотентно |
| `training.command_applied.v1` | training → read views/SSE | Sequence + safe public projection; никаких private facts |
| `training.turn_committed.v1` | training → media/SSE | `turn_id`, message ref; playback dedup + epoch |
| `teaching.lesson_stop_requested.v1` | teaching → close worker/media | Trigger only; источник истины — lesson barrier |
| `training.item_submitted.v1` | training → assessment/catalog candidate | Snapshot evidence; новый candidate не auto-published |
| `training.run_sealed.v1` | training → assessment/reporting | Create missing assessment keyed by evidence+rubric |
| `assessment.revision_ready.v1` | assessment → reporting | Replace contribution if revision newer, no double count |
| `operations.job_failed.v1` | ops/execution → monitoring | Alert sanitized category; no payload in metrics |

Tasks: `scenario.generate`, `scenario.validate_semantics`, `grammar.check`, `material.extract`, `simulator.turn`, `run.seal`, `lesson.close`, `assessment.evaluate`, `assessment.finalize`, `report.build`, `insight.generate`, `media.convert`, `backup.run`, `projection.reconcile`. Это закрытый реестр с валидатором входа, resource/time limits, retry/cancel policy; API не принимает произвольный kind от клиента.

Outbox и task enqueue внутри **одной локальной транзакции** нужны там, где бизнес-факт непосредственно порождает обязательную работу. Consumer использует inbox key `(consumer,event_id)` в транзакции с изменением своей проекции. Periodic reconcilers находят sealed runs без assessments/ready results без report projection, исправляют через dedup-key; это защита от ошибок доставки, не обход инвариантов.

## 6. Политика прав

| Действие | Operator | Instructor | Admin |
|---|---|---|---|
| Проходить назначение, видеть историю | Только своё | Preview в отдельном sandbox run | Нет по роли admin |
| Смотреть live transcript и evidence | Свой, с нужным release policy | Свои группы/занятия | Только явное support permission, с audit |
| Создавать/публиковать сценарий/карточки | Нет | Собственные/разрешённые | Нет по роли admin |
| Start/stop lesson | Нет | Только свой lesson | Только техническая остановка инфраструктуры, не оценивание |
| Изменить оценку | Нет | Свой scope + причина + revision | Нет |
| Управлять accounts/roles/config | Нет | Только состав своей группы | Явные IAM/config permissions |
| Чтение audit/backup/restore | Нет | Только аудит своей учебной операции при необходимости | Раздельные audit/backup/restore permissions |

Совмещение ролей пользователем допускается только явным назначением. Роль службы в учебном упражнении (`ServiceProfile`) не равна системной роли `operator`. Другой преподаватель не становится владельцем lesson потому, что он тоже instructor.
