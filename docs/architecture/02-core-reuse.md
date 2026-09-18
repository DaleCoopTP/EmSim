# Переиспользование orchestration core

## Вывод

Переиспользуем надёжный execution kernel и тестовые сценарии его гарантий. Учебную модель, interactive turn handler и многокритериальную оценку строим поверх адаптированной инфраструктуры. Текущий `/v1/runs` не является API учебной сессии.

Весь существенный код находится в `internal/`. Импорт из отдельного внешнего Go-модуля запрещён правилами Go; `replace` сам по себе это не исправляет. См. [правила internal packages](https://go.dev/doc/go1.4#internalpackages). Практический первый путь — зафиксированный source fork/перенос в один продуктовый Go-модуль с сохранением provenance. Публикация самостоятельной библиотеки kernel — последующая управляемая работа, а не условие запуска проекта. До распространения проверить происхождение и разрешение на переиспользование: в предоставленном дереве отдельный LICENSE не обнаружен; это не юридический вывод о правах пользователя.

## Проверенные ограничения

| Наблюдение | Доказательство в исходнике | Последствие |
|---|---|---|
| Один API key вместо пользователей | `internal/httpapi/api.go`, `authorized`, строки 69+ | Нужен новый IAM/RBAC, resource scopes, session revocation |
| Создание run сразу создаёт batch items/dialogue jobs | `internal/application/run/service.go`, `internal/postgres/run_create.go` | Занятие/оператор/интерактивный run — новая прикладная модель |
| Worker вызывает обе стороны диалога | `internal/application/dialogue/service.go`, `turnLoop`, строка 170 | Нельзя ждать человека через `ClientBot.Reply` |
| Pairs собираются в памяти, commit после цикла | Там же, `Handle`, `CommitDialogue` | Нет durable checkpoint каждой реплики; human path должен быть per-turn |
| Только `dialogue`/`judge` | `internal/queue/queue.go:29`, SQL `tasks_kind` | Требуется расширяемый реестр типов задач и новые SQL constraints |
| Lease содержит RunID/RunItemID/DialogueID | `internal/queue/queue.go:52` | Даже queue package пока не полностью универсален |
| `(kind, run_item_id)` уникален | `migrations/00001_orchestration_core.sql`, tasks | Нельзя добавить много turn tasks одного item без новой dedup-модели |
| Judge зависит от dialogue | `internal/application/judge/service.go`, SQL tasks/evaluations FKs | Оценка workflow-карточки не должна требовать фиктивного диалога |
| Scoring строго бинарный | `internal/application/judge/response.go:28`, SQL `evaluations_score` | Изменение только JSON недостаточно; нужны другой результат и schema |
| Нет cancel/stopped | `internal/domain/status.go`, SQL status/state_shape | Остановка преподавателем требует новых переходов и fencing |
| Recovery знает run_items | `internal/postgres/task_recovery.go`, `failItem:219` | Отказ технической задачи нужно маршрутизировать владельцу, не обрушать любой run |
| Finalizer проверяет dialogue+judge для каждого item | `internal/postgres/finalization.go:46+`; `summary.go` | Finalization protocol переиспользуется, бизнес-условия/summary меняются |
| Дефолт lease 120s, heartbeat 30s | `internal/recovery/policy.go:28` | Не доказывает восстановление ≤30s; frontend reconnect и worker recovery — разные бюджеты |
| Fixed finalization lease без heartbeat | `internal/application/finalization/policy.go` | Для нового тяжёлого sealing нужен bounded handler или heartbeat; LLM в finalizer запрещён |
| Локальный Compose без защищённых hop | `compose.yaml`: `sslmode=disable`, HTTP adapters | Не production-конфигурация для ТЗ, нужны TLS/CA/секреты/HA |

Линии относятся к предоставленному исходному дереву на дату анализа; контрольные hashes лежат в `source-inventory.json`.

## Матрица переноса

| Компонент | Решение | Конкретные изменения | Сохраняемые гарантии/проверки |
|---|---|---|---|
| `internal/queue`, `postgres/task_queue.go` | Адаптировать основу | Generic scope, typed input ref, business dedup key, cancel, DB clock | `SKIP LOCKED`, current-owner fencing, idempotent terminal digest |
| `internal/worker/runner.go` | Перенести с адаптацией | Kind registry, per-role policies, explicit cancelled outcome, time budgets | Bounded pool, heartbeat, drain, context cancellation |
| `internal/recovery` | Перенести протокол | Policies per kind, bounded inference deadlines, domain failure callback | Bounded retries/jitter, dead letter, lease reaping |
| `postgres/task_recovery.go` | Адаптировать SQL | Убрать обязательный `failItem`; atomic event owner notification | Stale owner cannot fail new owner; reaper concurrency |
| `domain/reproducibility.go`, snapshots | Перенести механизм | Versioned input manifests, classifier/rubric/model hashes | Immutable input, stable seeds, digest validation |
| Run create idempotency | Перенести паттерн | Scope key по actor+operation+resource; response receipt для всех команд | Same command returns same result; changed payload → conflict |
| `profiles` | Адаптировать | DB-published scenario versions вместо synthetic registry | Validated immutable snapshots; secrets excluded |
| Dialogue application | Заменить для human path | `simulator.turn`, `SendMessage`, checkpoint each turn | Старый loop можно сохранить только для автономных regression/evaluation прогонов |
| Inference HTTP adapter | Использовать как основу | Local caller/judge/STT/TTS ports, schemas, content limits, identity, HTTPS | Timeouts, safe errors, response bounds; wire format v1 не объявлять совместимым |
| Judge application/response | Заменить orchestration/evaluation contracts | Evidence по двум режимам, rubric components, revisions, unavailable | Schema validation, digest and fenced commit сохраняются как паттерн |
| Finalization | Адаптировать протокол, заменить eligibility | Sealed evidence; отдельная assessment finalization; stopped | Recoverable acquire/commit, immutable result; no wait-for-human/no LLM |
| `httpapi` | Новый public API, reuse middleware | User session, scope auth, resource errors, SSE, optimistic versions | request IDs, no-store, safe errors |
| Observability/admin endpoints | Перенести и расширить | Prefix/labels, queue by kind, inference latency, projection lag, stop latency | Low-cardinality metrics; correlation only in logs |
| Existing SQL migration | Не использовать как schema продукта | Новые schemas и forward migrations | Не ломать исходную runnable core fixture |
| Тесты | Перенести гарантийные cases | Новые fixtures/owners/statuses, stop races и commands | Lease theft, duplicate terminal, retry exhaustion, commit ambiguity |

Без «процента готовности»: число перенесённых строк не отражает готовность обучения. В исходном core отсутствуют lesson management, user identity, interactive UI, карточки/ЕКП, VoIP, multi-criterion assessment и product reports.

## Целевой execution contract

```text
Task {
  id, kind, scope_id, input_ref, input_digest,
  dedup_key, expected_interaction_epoch,
  status, attempts, max_attempts,
  lease_token, leased_worker, lease_expires_at,
  next_attempt_at, terminal_digest, safe_failure
}
```

`execution.scopes` даёт FK для ownership; scope имеет immutable `owner_type` и `owner_id`. Business tables регистрируют scope в той же транзакции; runtime integrity job выявляет orphan owners. Cancellation policy различается по kind. Уникальность `(scope_id, kind, dedup_key)`, например `item:{id}:turn:{turn_id}` либо `assessment:{id}:revision:{n}`. Новая revision/redrive — новая задача с `supersedes_task_id`; токен старой задачи не переиспользуется.

Delivery остаётся **at-least-once**. Атомарный commit effect + task terminal + outbox предотвращает повторный внутренний эффект. Внешний LLM вызов может повториться при неопределённом исходе; сравнение request ID/digest и stored receipt не превращает внешний сервис в exactly-once. Проверка после неоднозначного commit идёт через durable receipt, а не повторную генерацию вслепую.

Claims короткие и не держат DB locks на inference. При новом SQL используется время БД для expiry/commit checks, чтобы процессные часы не нарушали lease fencing. Global `lease_token=attempts` из старой схемы не навязывается cancellation: отмена переводит задачу в terminal и блокирует commit по статусу/epoch; новая попытка получает новый токен.

## Безопасный маршрут внедрения

1. Зафиксировать hash исходного дерева и запустить baseline tests в доступном окружении. В рамках этой архитектурной работы production core не изменялся и его suite не запускался.
2. Перенести kernel в продуктовый модуль; сначала сохранить существующий synthetic pipeline как отдельный regression harness.
3. Создать **новую** schema `execution` и contract tests с `scope_id`, cancellation и повторяющимися turns. Новые tasks не записывать в старую таблицу.
4. Подключить `training` и `assessment` handlers. Проверить crash boundaries до первого LLM API.
5. При существующих исторических core runs оставить read-only legacy API или импортировать их как отдельные benchmark records. Не изображать их учебными попытками без operator/assignment/evidence.
6. Выделять kernel в внешний Go module только после стабилизации границы и права на распространение; это отдельный ADR, не блокер продукта.
