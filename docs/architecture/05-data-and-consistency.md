# Данные, транзакции и отказоустойчивость

## 1. Модель хранения

Один кластер PostgreSQL, schemas по владельцам. Совместное физическое хранение позволяет атомарно изменять учебный факт и ставить задачу; границы модулей защищены кодовыми портами, database grants и проверками импортов. Cross-schema FK внутри продукта разрешены для неизменяемой идентичности; чтение/изменение чужой таблицы из произвольного domain package запрещено.

| Schema / таблицы | Ключевые поля / ограничения | Владелец |
|---|---|---|
| `identity.users` | `id`, unique local/IdP subject, status, display name; soft delete | IAM |
| `identity.roles`, `user_permissions`, `sessions` | Scope-aware grants, hashed session token, expiry, revocation_version | IAM |
| `teaching.groups`, `group_members` | Instructor owner, unique membership; historical assignments не меняются при выходе из группы | Teaching |
| `curriculum.scenarios`, `scenario_versions` | Unique `(scenario_id,version)`, draft/published/archived, difficulty, mode, content digest, approved_by | Curriculum |
| `curriculum.rubric_versions`, `workflow_versions` | Immutable definitions + digest; только опубликованные версии в assignments | Curriculum |
| `curriculum.materials`, `material_versions`, `generation_jobs` | Immutable blob ref/digest, extraction status, prompt/model manifest, generated draft ref | Curriculum |
| `catalog.classifier_versions`, `incident_types`, `features`, `routing_rules` | Version+code keys, conditions, service_id, source sheet/cell, validation report | Catalog |
| `catalog.services`, `service_profiles` | System role отдельно; territory/competence ref + policy version | Catalog |
| `catalog.card_templates`, `card_template_versions` | source=`system|student`, source_item_id?, redaction report, approved_by, snapshot+digest | Catalog |
| `teaching.lessons` | owner, group, mode, state, version, interaction_epoch, stopped_at, reason | Teaching |
| `teaching.lesson_plans`, `lesson_source_pool` | Frozen filters/weights/exhaustion/seed/timings, source version IDs, stable selection order | Teaching |
| `teaching.assignments` | lesson+operator+service_profile, attempt policy; FK snapshots | Teaching |
| `training.runs` | assignment/operator, state, version, epoch, active_item_id, event_seq, snapshot_id, terminal_reason | Training |
| `training.run_snapshots` | Canonical bytes + digest, scenario/rubric/classifier/workflow manifests, seed; immutable | Training |
| `training.run_items` | unique `(run_id,ordinal)`, selected_source_version, state, started/submitted timestamps, deadline | Training |
| `training.item_snapshots` | item_id PK, frozen scenario/source/reference/rubric, canonical digest, inherited run policy version | Training |
| `training.messages` | id, run/item, conversation turn, speaker, text, delivered status; unique `(item_id,turn_id,speaker)` | Training |
| `training.conversations` | item_id PK, pending_turn_id, conversation_epoch, disclosure_state, next_turn_no | Training |
| `training.incident_cards` | item_id PK, version, structured fields, initial_source_digest, registration stage | Training |
| `training.service_reactions`, `reaction_history` | `(item_id,service_id)` PK, status/version; append-only history + comment + actor + time | Training |
| `training.actions` | action ID, command ID, item, accepted/rejected, safe payload, server/client timestamps | Training |
| `training.events` | PK `(run_id,seq)`, unique event_id, event type/public payload ref, created_at | Training |
| `training.command_receipts` | Unique `(actor_id,operation,resource_id,command_id)`, payload hash, result, HTTP status | Training/common command layer |
| `training.deadlines`, `recovered_inputs` | due_at/policy; late offline input id/digest/disposition, sealed cutoff ref | Training |
| `training.evidence_manifests` | unique `(item_id,evidence_revision)` or run manifest; cutoff, canonical bytes+digest; immutable | Training |
| `assessment.assessments`, `assessment_attempts` | Evidence/rubric/evaluator refs, revision key, task refs, technical state, safe failures | Assessment |
| `assessment.criterion_results`, `assessment_revisions` | Components/evidence refs, score nullable, automatic/expert, supersedes, reason, published_by | Assessment |
| `reporting.run_results`, `operator_progress`, `group_progress` | Snapshot/revision refs, completeness, updated_at; replacement contributions | Reporting |
| `reporting.report_jobs`, `group_insights` | Scope, filter, input frontier/hash, operation state, output blob ref, generated_at | Reporting |
| `media.sessions`, `media.chunks`, `media.playbacks` | run/item, endpoint/connection epoch, chunk ordinal+digest, stable playback_id/delivered ranges | Media adapter |
| `storage.blobs` | Immutable content digest, object key/version, size/type, availability state, owner scope | Storage |
| `execution.scopes`, `execution.tasks` | owner type/id, unique `(scope,kind,dedup_key)`, leases/retries, status/cancel policy | Execution |
| `eventing.outbox`, `eventing.inbox` | event_id; pending/delivered state; unique `(consumer,event_id)` | Eventing |
| `audit.events` | append-only, monthly partition, actor/time/action/scope/outcome/refs, redacted diff | Operations |
| `operations.jobs`, `configuration_versions`, `backup_manifests` | Approved action/params, before/after versions, backup checksums, restore report | Operations |

PK для пользовательских сущностей — UUID. Code-level JSON IDs — строки, даты — RFC3339 UTC, длительности wire — integer milliseconds. DB durations — integer milliseconds или interval с однозначным преобразованием. Balances/score arithmetic — fixed precision/numeric, не float накопление в БД; `float64` в sketch ports — только DTO.

### Обязательные ограничения

- FK run→assignment→lesson и run→operator; связь оператора должна совпадать с assignment (composite FK либо scoped transaction invariant + проверяемый DB constraint/trigger).
- `UNIQUE(assignment_id,attempt_no)`; частичный unique для одной активной попытки per assignment по default policy.
- Partial unique `(run_id) WHERE item_state IN ('offered','in_progress')`: одна активная карточка.
- `UNIQUE(item_id,turn_id)` для simulator task input и output identity; input digest неизменен после enqueue.
- Cross-reference item/run проверяется composite FK, чтобы нельзя было записать чужой item в события другого run.
- Все published snapshots/results/evidence запрещены UPDATE/DELETE для runtime role; новая версия отдельной строкой.
- `CHECK score IS NULL OR score BETWEEN 0 AND 100`; unavailable не имеет финального verdict; check complete result shape.
- Удаление пользователей/сценариев через archive, не каскадное уничтожение runs. Retention cleanup — отдельная операция с backup/checks.
- Lease constraints соответствуют state: pending без owner; leased с future expiry/owner/token; terminal с digest/reason; cancelled допускается без попытки выполнения.
- `execution.scope_id` — FK из task и owning record. Полиморфный `owner_type/id` не объявляется магическим FK к любой таблице: создание scope+owner атомарно, consistency checker проверяет обратную сторону.

### Индексы запросов

`tasks(kind,not_before,created_at,id) WHERE status='pending'`; `tasks(lease_expires_at,id) WHERE status='leased'`; `tasks(scope_id,kind,status)`; `runs(operator_id,created_at DESC,id)`; `runs(lesson_id,state)` (denormalized lesson ID с FK consistency); `assignments(operator_id,state)`; `events(run_id,seq)`; `messages(item_id,server_at,id)`; `deadlines(due_at,id) WHERE pending`; `assessment(evidence_digest,rubric_version,evaluator_revision)` unique; `run_results(operator_id,ended_at DESC,run_id)`; `audit` by partition+actor/time and scope/time; `outbox(available_at,id) WHERE undelivered`.

B-tree indexes на реальные predicates, bounded pagination; GIN на JSON только после конкретного измеренного запроса. Transcript не сканируется при построении отчёта. Pool budget рассчитывается как сумма replicas×pool_size + workers + admin/migrations + reserve, а не «по 100 connections каждому».

## 2. Атомарные операции

| Операция | Один DB commit содержит |
|---|---|
| Start lesson | Проверка опубликованного плана, frozen pool, status/epoch, audit, outbox, receipt |
| Start run | Assignment validation, scope, snapshot, run/item, initial task, event, audit, receipt |
| Apply operator command | Barrier/version/idempotency, action/message/card/reaction, run version+seq, new task if needed, audit, receipt |
| Commit simulator turn | Barrier/turn/lease validation, message/disclosure, task done/digest, event, audit |
| Submit item | Final card/state, item evidence cutoff/manifest, unique assessment request, event, audit, receipt |
| Offer next | Lesson/run check, stable pool selection cursor, item+card+reaction+deadline, event, receipt |
| Stop lesson | Barrier epoch/state, stop time/reason, close job, audit, event, receipt |
| Seal run | Frozen evidence, current item interruption if needed, interaction-task cancellation, terminal run, assessment reconciliation task, event |
| Finalize assessment | Criterion result/revision, immutable summary, task completion, outbox |
| Review assessment | New expert revision, reason, audit, event; previous immutable revision unchanged |
| Consume projection event | Inbox receipt + replace contribution + projection watermark |

Item assessments могут стартовать после каждого SubmitItem. Run manifest после seal ссылается на уже созданные item evidence/assessments и создаёт только недостающие для прерванного item. Run-result aggregator объединяет item revisions; повторно оценивать все завершённые карточки не нужно. Ожидание недостающей оценки не занимает worker и не расходует retry budget: event-triggered eligibility scan создаёт finalization только при готовых/terminal evaluation states; периодический scanner восстанавливает пропущенный trigger.

## 3. Порядок блокировок и гонка stop

Стандартный порядок: **lesson (`FOR SHARE` для интерактивной команды / `FOR UPDATE` для stop) → run (`FOR UPDATE`) → item/conversation → task → sequence/receipt/event inserts**. Все обработчики, включая terminal failure path, соблюдают порядок. Claim/reaper сначала выполняют отдельную короткую task-only транзакцию; они не держат task lock, пока ждут lesson/run. Если recovery должна изменить доменное состояние, она публикует факт или отдельно берёт locks в доменном порядке.

`FOR KEY SHARE` не подходит как stop barrier: он не блокирует любое изменение состояния lesson. Используется `FOR SHARE` либо эквивалентная явно доказанная сериализация. [PostgreSQL locking и SKIP LOCKED](https://www.postgresql.org/docs/current/sql-select.html) позволяют использовать пропуск занятых строк для очереди; такой SELECT не заменяет согласованное чтение бизнес-состояния.

Упрощённая логика `CommitTurn`:

```text
BEGIN
  SELECT lesson FOR SHARE; require running && epoch == captured_lesson_epoch
  SELECT run FOR UPDATE; require active && epoch == captured_run_epoch
  SELECT conversation FOR UPDATE; require pending_turn_id == output.turn_id
  SELECT task FOR UPDATE; require leased && worker/token match
  require lease_expires_at > database_clock_now
  INSERT message IF this turn does not already exist
  UPDATE disclosure, conversation; clear pending_turn_id
  increment run version and event_seq
  INSERT public event, audit, outbox; mark task done with digest
COMMIT
```

Любой guard failure откатывает эффект. Если вывод уже записан с тем же digest, возвращается `already_applied`; другой digest — integrity conflict. Task `cancelled` после stop не остаётся бесконечно leased. Handler получает typed cancellation, а не `failed`/плохую оценку. Физический inference может ещё завершаться, но его поздний ответ не попадёт в run.

Stop lesson не обходит все runs под одной долгой транзакцией: общий barrier делает дальнейшие интерактивные commits невозможными, а durable close job закрывает runs небольшими batches. API возвращает `stopping` и `stop_effective_at`; UI сразу прекращает действия. Maintenance повторно подбирает lesson `stopping` после crash. Default технический budget на sealing metadata <30с в нагрузочном тесте; это проектная цель, stop barrier должен укладываться в API/UI ≤2с.

Гонка `SubmitItem`/`StopLesson`: кто первым завершил защищённую транзакцию, определяет границу. Submit до stop сохраняется целиком; после stop уходит в recovered-only при необходимости. Гонка `OfferNextCard`/stop аналогична: до stop карточка может успеть появиться и будет зафиксирована как interrupted; после stop новая не создаётся. `StopRun` берёт lesson shared, затем конкретный run exclusive и увеличивает run epoch.

## 4. Reconnect и идемпотентность

1. UI сохраняет полный intent и `command_id` локально до отправки. Запись должна завершиться, прежде чем кнопка показывает buffered; quota failure виден пользователю.
2. Server commit является точкой принятия; transport ACK может потеряться.
3. Клиент на повторном входе проверяет пользователя, получает snapshot с атомарным `last_event_seq` и статусом. Локальная очередь scoped по user+run, не передаётся новому пользователю общим компьютером.
4. Повторяет команды по client_seq. Receipt находится до проверки устаревшей expected version (после authorization), поэтому потерянный ACK не даёт ложного version conflict.
5. После новой конфликтующей команды автоматическая последовательность останавливается. Draft сохраняется; пользователь/преподаватель разбирает несогласованные действия. Нельзя молча менять sequence/ожидаемую версию старого intent.
6. Terminal run принимает поздние данные только через supplemental intake. Они не меняют frozen score; UI явно показывает «восстановлено, требует разбора», а не «выполнено вовремя».

Receipt retention как минимум lifetime активного run + объявленное окно offline replay; до принятия общей policy хранить вместе с run, чтобы длинная отсроченная команда не повторила действие после очистки ключа. Logout не уничтожает неподтверждённые данные без предупреждения; предусмотрено восстановление после повторной аутентификации того же пользователя. На общей станции данные очищаются после ACK/завершения согласно политике, secrets в IndexedDB не хранятся.

Event sequence выделяется **в транзакции под run lock**. Обычный глобальный `bigserial` не используется как безусловный commit-order cursor: две транзакции могут зафиксироваться в другом порядке. Для run stream `(run_id,seq)` упорядочен той же блокировкой. Aggregate payload для SSE отфильтрован по роли; cursor никогда не разрешает обойти доступ. При слишком старом cursor сервер возвращает snapshot-required, а не тихо пропускает события.

Outbox читается по признаку недоставленности с lease/SKIP LOCKED и доставляется at-least-once; он не полагается на глобальный max ID. Consumers обрабатывают versioned aggregate events идемпотентно; если нужно строгое агрегатное упорядочение, gap откладывается и запрашивается недостающее событие. Progress projection учитывает номер assessment revision и не применяет старую после новой.

## 5. Медиа и БД не образуют общую транзакцию

Blob upload: временный объект → checksum verification → immutable object/version → manifest row `available` → доменная ссылка. При crash до manifest объект попадает в GC после grace period; при отсутствии объекта после DB commit читатель видит `unavailable` и alert, не ложное «записано». Garbage collection не удаляет referenced objects. Backup manifest указывает точные object versions.

Chunks имеют `(media_session_id,track_id,chunk_no)` unique и digest. Повтор с тем же digest принят, с другим — integrity conflict. Server storage и локальный capture могут иметь разные дорожки; manifest сохраняет источник, time range и gap indicators. Отложенный chunk не вставляется в закрытую оценку автоматически. При потере клиента/диска до серверного ACK локальный буфер не является абсолютной гарантией — это отдельная граница отказа от краткого сетевого сбоя.

## 6. Recovery matrix

| Сбой | Что уже сохранено | Восстановление |
|---|---|---|
| API упал до commit | Ничего на сервере, intent на клиенте | Replay same command ID |
| API упал после commit до ACK | State + receipt + tasks | Return receipt, не повторять эффект |
| Worker упал до LLM | Input + pending/leased task | Lease expiry/retry |
| LLM ответил, worker упал до commit | Input сохранён, результат мог потеряться | Повтор inference возможен; один fenced domain commit |
| Ответ записан, worker потерял ACK | Message + done receipt/digest | Reconcile persisted terminal state, не новый turn |
| Stop во время inference | Lesson barrier + close job | Abort request best effort, discard stale commit, seal |
| Maintenance упал при sealing | Barrier + частично sealed runs | Идемпотентно продолжить batches, unique evidence keys |
| Judge недоступен | Sealed evidence + status pending/unavailable | Retry bounded, потом needs_review; run не исчезает |
| Projection consumer упал | Outbox + возможно inbox/updated projection | Dedup+reconcile; freshness surfaced |
| DB временно недоступна | Клиентская очередь, последние ACK | Никаких новых ACK до commit; восстановить и replay |
| DB primary потерян | Зависит от durability profile | Sync standby promotion + fencing; async replica не доказывает RPO=0 |
| Media node потерян | Сегменты в object store / local capture | Новая media binding, gap/replay policy, повторное подключение |
| Storage заполнен | Старые данные сохранны | Reject new recording/unsafe writes; pause/admission; alert |

Короткий worker lease на interactive metadata не обязан покрывать весь долгий inference: heartbeat продлевает lease, а absolute inference timeout ограничивает его жизненный цикл. Параметры 10–15с lease, 2–4с heartbeat и 1–2с reaper — **кандидаты для spike**, не готовая гарантия. При длинной GC/network pause возможен duplicate compute, но fencing сохраняет один effect. Control reconnect не ждёт worker lease expiry.

## 7. Хранение, backup, HA

Обязательный security retention — минимум 6 календарных месяцев. При monthly partition drop проверяется максимум timestamp раздела относительно cutoff; «180 дней» не подменяет 6 месяцев. Runtime role не удаляет audit; raw sensitive payload не дублируется в security log. Неограниченное хранение транскриптов/аудио не объявляется требованием: policy утверждается для каждого класса данных, ссылки в старых отчётах корректно отражают удаление по retention.

Daily full/logical backup + WAL/PITR + immutable object versions на независимом узле. Для согласованности DB/blob snapshot используется manifest на backup cutoff с набором referenced immutable objects и проверкой их наличия, либо документированная write quiescence при full snapshot. Просто два независимых cron export без общего manifest недостаточны. Ключи шифрования/CA/session configuration копируются отдельно с ограничением доступа.

HA требует кворума/consensus для единственного writable DB primary и fencing старого. Две ноды без механизма разрешения split-brain не гарантируют сохранность. При невозможности sync commit система временно перестаёт подтверждать writes; это сознательный выбор сохранности данных. Конкретный HA manager и object storage продукт выбираются в W0, interface/requirements зафиксированы здесь. Failure одного API не теряет state; failure целого единственного inference host уменьшает способность обслуживать звонки, поэтому admission и резерв мощности входят в HA профиль.
