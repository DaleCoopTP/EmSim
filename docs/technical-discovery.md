# EmSim — technical discovery: что переиспользуем из orchestration-core

Версия 1.0 · 17 сентября 2026 · вход: `product-task.md`; объект: `../orchestration-core-main` (Go-модуль `github.com/DaleCoopTP/orchestration-core`, ~7,3 k строк кода + 7,5 k строк тестов).

Проверено чтением: `README.md`, `migrations/00001_orchestration_core.sql`, `internal/{queue,worker,recovery,postgres,httpapi,observability,inference,domain,application/*}`, `cmd/*`, `compose.yaml`, `Dockerfile`, `Makefile`, `.github/workflows/verify.yml`, имена интеграционных тестов. Тесты не запускались (нужен Docker).

---

## 1. Что такое orchestration-core

Сервис для **воспроизводимых батч-оценок LLM-диалогов**: `POST /v1/runs` создаёт run из N синтетических items; worker `dialogue` прогоняет диалог «симулятор ↔ бот» по профилю; worker `judge` оценивает транскрипт (вердикт pass/fail); worker `maintenance` подбирает просроченные lease и финализирует run в один JSON-результат. Всё на PostgreSQL: очередь задач с lease/heartbeat/fencing/retry/dead-letter, идемпотентное создание run, детерминированные seed'ы.

Стек: Go 1.26, `pgx/v5`, `goose` (миграции, embed), `google/uuid` (v7), `prometheus/client_golang`, `slog`; тесты — `testcontainers-go` (PostgreSQL 16); поставка — multi-stage `Dockerfile` (alpine, non-root), `compose.yaml`, `Makefile` с воротами `verify` (gofmt / build / test / vet / staticcheck), CI на GitHub Actions (verify → integration → e2e).

Качество: высокое для инфраструктурной части — CHECK-ограничения на форму состояния каждой таблицы, fencing по `(worker, lease_token)`, «один победитель» между terminal-writer и reaper, ограниченный батч reaper, интеграционные тесты на каждую гонку (16 кейсов по очереди и recovery).

---

## 2. Ответы на вопросы discovery

**Нужен новый сервис или расширяем существующий?** Новый модуль. Core — не библиотека и не платформа: весь код в `internal/` (Go запрещает импорт `internal/` из другого модуля), схема БД жёстко завязана на `runs/run_items/dialogues/evaluations`, API — один эндпоинт под API-ключ. Домен (диалог бота с симулятором, вердикт 0/10000) не совпадает с нашим ни в одной сущности. Переиспользование = **копирование выбранных файлов в новый репозиторий с адаптацией**, не зависимость.

**Owner.** Одна команда, один репозиторий `emsim`, один Go-модуль. Core остаётся как есть в качестве источника кода и как «референс качества» для наших тестов на очередь.

**Что стандартно (наследуем как решения без обсуждения).** Go 1.26 · PostgreSQL 16 · `pgx/v5` + `pgxpool` · `goose` с embed-миграциями · UUID v7 · `slog` JSON · Prometheus `/metrics` + `/healthz` + `/readyz` на отдельном admin-порту · alpine multi-stage образ, non-root · compose с healthcheck и `depends_on: service_healthy` · `make verify` как pre-commit ворота · testcontainers для интеграционных тестов.

**Какие внутренние API переиспользуем.** Нет сетевых API, которые можно вызвать: `POST /v1/runs` нам бесполезен. Переиспользуем код и SQL-паттерны (таблица ниже).

---

## 3. Таблица переиспользования

Режимы: **as-is** — копируем файл, меняем import path; **адаптация** — копируем и правим (объём указан); **паттерн** — берём подход и SQL, код пишем свой; **нет** — не берём.

### 3.1 Очередь задач и worker — главное, что берём

| Файл | Строк | Режим | Что менять |
|---|---|---|---|
| `internal/queue/queue.go` | 144 | адаптация | `Kind` — из enum `dialogue\|judge` в открытую строку с реестром; из `Lease` убрать `RunID/RunItemID/DialogueID`, добавить `ScopeID`, `Payload []byte` |
| `internal/postgres/task_queue.go` | 198 | адаптация | те же поля в `RETURNING`; SQL claim/heartbeat/terminal — без изменений (`FOR UPDATE SKIP LOCKED`, fencing по `leased_worker AND lease_token AND lease_expires_at > now`) |
| `internal/postgres/task_recovery.go` | 229 | адаптация | убрать `failItem` (в core провал задачи = провал run_item; у нас провал `assessment.evaluate` ≠ провал карточки — карточка получает статус оценки `unavailable`, обучаемый не страдает). Это **семантическое** отличие, не косметика |
| `internal/recovery/{policy,failure,lifecycle,supervisor}.go` | 340 | адаптация | `RetryDelay(kind)` различает `Dialogue/Judge` — заменить на базу retry по реестру kind'ов. Дефолты (lease 120 с, heartbeat 30 с, 3 попытки, cap 5 мин) годятся; для `scenario.generate` на CPU lease поднять до 10 мин |
| `internal/worker/runner.go` | 305 | as-is | claim → горутина → heartbeat → потеря lease отменяет handler → drain при остановке. Ни одной доменной ссылки |
| `internal/worker/roles.go` | 105 | адаптация | роли `dialogue/judge/maintenance/all` → `worker` (все kind'ы) + `maintenance`; `Composite` — as-is |
| `migrations` — таблица `tasks` | ~60 | паттерн | брать **колонки и CHECK'и** (`tasks_attempt_budget`, `tasks_lease_shape`, `tasks_state_shape`) и три индекса (`pending_claim`, `leased_expiry`); **выбросить** FK на `runs/run_items`, `dialogue_id`, `UNIQUE(kind, run_item_id)`, семь составных UNIQUE под deferred-FK. Добавить `scope_type/scope_id`, `dedup_key UNIQUE`, `payload jsonb`, `result jsonb` |
| `test/integration/task_queue_test.go`, `task_recovery_test.go` | ~450 | адаптация | 16 кейсов — переписать на новую таблицу; это наш регрессионный набор на очередь. Харнес на testcontainers — as-is |

Итого ~1 400 строк кода + ~450 строк тестов, из них правится, по оценке, 20–25 %. Это **готовая durable-очередь за день работы вместо недели**.

Ключевое наблюдение по схеме: в core `tasks` не самостоятельная таблица, а ребро графа `run → item → task → dialogue/evaluation`, скреплённое deferred-FK по составным ключам (`tasks_dialogue_identity_fkey`, `dialogues_task_identity_fkey`). Это красиво для воспроизводимости батча, но для нас — лишняя жёсткость: у нас задача ссылается на карточку/сценарий/занятие по `scope`, и результат задачи живёт в доменной таблице (`assessments`, `scenario_versions`), а не в FK от `tasks`.

### 3.2 Процесс, наблюдаемость, поставка — берём

| Файл | Режим | Что менять |
|---|---|---|
| `internal/httpapi/admin.go` (`/healthz`, `/readyz`, `/metrics`) | as-is | — |
| `internal/observability/http.go` (`InstrumentHTTP`, response recorder с Flusher/Hijacker) | as-is | поддержка `Flush()` уже есть — важно для SSE |
| `internal/observability/metrics.go` | адаптация | валидаторы label'ов (`validKind`, `validRole`, `validRoute`) — жёсткие списки; заменить на реестр |
| `internal/observability/logging.go` | адаптация | `Operation()` принимает только 3 операции и 2 кода — это защита от утечек, но для нас слишком узко; оставить принцип «в лог — только whitelisted поля, никогда — текст обучаемого», расширить список |
| `internal/observability/sampler.go` (gauge'и очереди раз в 15 с) | адаптация | запросы к `tasks` под новую схему |
| `internal/postgres/postgres.go` (`Open/Ping/Up/Down/CurrentVersion`, `ExpectedSchemaVersion`) + `migrations/embed.go` + `cmd/migrate` | as-is | номер ожидаемой версии |
| `Dockerfile` | адаптация | четыре бинарника → один `emsim` + фронтенд-стадия (node build → статика) |
| `compose.yaml` | паттерн | структура (anchors, healthcheck, `service_completed_successfully` для migrate) — да; сервисы — свои: `postgres, migrate, emsim-api, emsim-worker, llm, stt, caddy` |
| `Makefile`, `.github/workflows/verify.yml` | as-is | добавить `web` (lint/build) и `go test -tags=integration` |
| `internal/config/*.go` (env → struct с валидацией) | паттерн | свои переменные; подход «падать на старте при невалидной конфигурации» — да |

### 3.3 HTTP API — только паттерны

`internal/httpapi/api.go` — 234 строки под один маршрут и один API-ключ. Роутера нет, сессий нет, ролей нет. Берём **приёмы**: `X-Request-ID` на каждый ответ, `Cache-Control: no-store`, единый JSON-конверт ошибки `{error:{code,message}, request_id}` с whitelist'ом кодов, `DisallowUnknownFields` + проверка «после JSON ничего нет», `LimitReader` на тело, `Idempotency-Key` + hash запроса → replay/conflict (нам это же нужно для `command_id` в действиях обучаемого). Код пишем свой на `net/http` + `chi`.

### 3.4 Инференс — только транспортный скелет

`internal/inference/http/http.go` (147 строк): таймаут, лимит тела, классификация ответа (`408/429/5xx` → retryable `ErrRemoteFailure`, прочие не-2xx → permanent `ErrRemoteRejected`), строгий JSON. Это нужно и нам для `llama.cpp`/`whisper.cpp`. Но протокол `inference/v1` с `Identity{RunID, ItemID, DialogueID, Turn}` и запросы `Simulator/Client/Judge` — не наши: у нас OpenAI-совместимый `/v1/chat/completions` с JSON-схемой ответа. `deterministic/` и `replay/` адаптеры — идея хороша (детерминированный судья для тестов и демо без модели), реализация не переносится.

### 3.5 Домен — не берём

| Пакет | Строк | Почему нет |
|---|---|---|
| `application/run` + `postgres/run_create,run_read` | ~500 | run = батч из N синтетических сэмплов с seed'ами и snapshot профиля. У нас «run» — прохождение занятия живым человеком; общего — только слово |
| `application/dialogue` + `postgres/dialogue` | ~500 | цикл ходов бот↔симулятор. У нас диалога нет (заказчик снял) |
| `application/judge` + `postgres/judge` | ~520 | вердикт `pass/fail`, `score_basis_points IN (0, 10000)` — бинарная оценка. Нам нужна рубрика из 8–10 критериев с весами, частичным зачётом и ревизиями преподавателя |
| `application/finalization` + `postgres/finalization` | ~900 | финализация run в один JSON с lease на строке `runs`. У нас «занятие завершено» — это stop преподавателя + закрытие карточек, другая машина состояний |
| `profiles/`, `domain/profile.go`, `domain/reproducibility.go` | ~600 | профили и seed'ы для воспроизводимости — не требование |
| `cmd/dummybot` | 214 | заглушка бота |
| `domain/status.go`, `domain/failure.go` (`SafeFailure`, `FailureStage`) | ~200 | паттерн `SafeFailure{stage, code}` с regex `^[a-z0-9_]{1,64}$` — берём как идею для безопасных кодов ошибок; сами стадии свои |

Итого доменного кода ~6 400 строк — не переносится. Это ожидаемо: core решал другую задачу.

---

## 4. Про догадку «run / run_item и похожее»

Частично. Схема «контейнер → элементы → задачи по элементам → результат по элементу» у нас тоже есть (занятие → карточки → задачи оценки → оценки), но с двумя отличиями, которые делают копирование таблиц вредным:

1. В core элемент **пассивен**: его целиком двигает worker (dialogue → judge), человека нет, поэтому `run_items.status` = `pending → dialogue_done → scored`. У нас элемент (карточку) минутами двигает **человек** через API, а worker подключается только в конце. Значит `items` — доменная таблица training с собственными состояниями (`offered → opened → in_progress → closed/interrupted`), действиями, дедлайнами, и очередь к ней отношения не имеет.
2. В core `tasks` намертво привязана к `run_items` (FK, `UNIQUE(kind, run_item_id)`, `run_item_id NOT NULL`). У нас задачи бывают и без карточки: `scenario.generate` (scope = сценарий), `voice.render`, `ekp.import`, `report.build`, `backup.run`. Поэтому `tasks.scope` — полиморфный, а не FK.

Так что: **очередь — да, таблица `tasks` — по образцу, `runs/run_items` — нет.**

---

## 5. Чего в core нет и что придётся делать с нуля

Для понимания реального объёма. Ничего из этого core не покрывает даже частично:

- Пользователи, сессии по cookie, роли, рабочие места, RBAC.
- Доменная модель: сценарий/версии, занятие, назначения, карточка, действия, события по таймлайну, звонки, оценки, ревизии, рекомендации.
- Планировщик событий сценария (tick 500 мс, `due_at`, SKIP LOCKED) — похож на reaper, пишется своим.
- SSE-шина (in-process + `pg NOTIFY` при нескольких api).
- Хранение бинарников (записи звонков, озвучка): файловая директория + `blobs` с digest.
- Импорт классификатора XLSX, справочник служб и их workflow.
- Клиенты LLM/STT/TTS с JSON-схемами и промптами.
- Детерминированный оценщик + рубрика + расчёт балла + политика рекомендации уровня.
- Отчёты (CSV/PDF) и графики.
- **Весь фронтенд** (копия АРМ-112, телефон, монитор, ЛК, редактор сценариев, отчёты).

По времени фронтенд и доменная модель — 80 % работы; очередь из core экономит ~5 %. Это надо честно понимать: core снимает один риск (надёжный фон для LLM-задач), а не «половину бэкенда».

---

## 6. Риски переноса

| Риск | Смягчение |
|---|---|
| Скопированный код со временем расходится с core; правки в core не подтягиваются | Принять: копируем один раз, в заголовке файла — ссылка на исходный commit. Core больше не развивается под нас |
| Слишком строгие whitelist'ы (logger, metrics) заставят «расширять список» при каждом новом kind | Реестр kind'ов в одном месте (`platform/tasks/kinds.go`), валидаторы читают его |
| Lease 120 с мал для генерации сценария на CPU (минуты) | Lease per-kind в реестре; heartbeat уже есть, так что достаточно поднять lease до 10 мин |
| Провал задачи оценки уронит карточку, если бездумно перенести `failItem` | Убрать; статус оценки — отдельная таблица, карточка не знает о задачах |
| Тесты core на очередь используют `runs/run_items` фикстуры | Переписать фикстуры под `scope`; логика проверок остаётся |
| testcontainers требует Docker у каждого разработчика | Да; альтернатива — `DATABASE_URL` на локальный PostgreSQL через env в том же харнесе |

---

## 7. Решения, которые фиксируем по итогам discovery

1. Новый репозиторий `emsim`, один Go-модуль, один бинарник `emsim {api|worker|migrate|import}`; фронтенд в `web/`.
2. Из core переносим копированием: очередь + worker + recovery (~1,4 k строк, адаптация ~25 %), admin-эндпоинты, HTTP-инструментирование, postgres bootstrap + migrate, Dockerfile/Makefile/CI-скелет, интеграционные тесты очереди.
3. Таблица `tasks` — по образцу core с полиморфным `scope`, `dedup_key`, `payload`; без FK на доменные таблицы.
4. Провал фоновой задачи никогда не меняет состояние карточки/занятия — только статус соответствующего результата.
5. Доменный код core (run/dialogue/judge/finalization/profiles/inference-протокол) не переносится.
6. HTTP-слой и клиенты моделей пишутся заново по паттернам core (request-id, безопасные коды ошибок, строгий JSON, таймауты/лимиты, классификация retryable/permanent).

Следующий шаг по методике — design doc (пункт 3): границы модулей, API, схема данных, критические потоки, consistency/failure semantics — с опорой на эти решения.
