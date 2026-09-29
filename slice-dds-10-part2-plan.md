# План среза ДДС-10 (часть 2): роль администратора

Дата: 2026-09-29. Статус: план (коммиты c0–c19). Решение и обоснование — [ADR-038](design-docs/adr/038-admin-role.md).
## Контекст

В ТЗ заказчика для роли «Администратор системы» есть пункты, которые EmSim пока не закрывает. Уже сделано: пользователи и роли, блокировка через `active`, рабочие места, экран «Состояние» (очередь, модели, воркеры, диск, упавшие задачи с повтором), ежедневный и ручной бэкап, журнал аудита, который пишется в той же транзакции, что и действие, `audit.prune`. Минимальные привилегии соблюдены по устройству ролей: у админа нет доступа к сценариям, занятиям, оценкам и отчётам (`internal/auth/authz.go`).

Не хватает: просмотра журнала аудита, картины нагрузки, статистики использования, отчёта о сбоях, просмотра конфигурации, контроля целостности, политики входа, массового создания пользователей и процедуры обновления с обязательной копией.

Решения пользователя от 29.09.2026:
1. Сервисы из веба не запускаются и не останавливаются: без docker socket, управление через `make`/compose по инструкции, плюс «режим обслуживания», который запрещает старт новых занятий.
2. Настройку «IP-телефонии» пропускаем совсем, ничего не делаем.
3. Базовые настройки безопасности (TLS, срок сессии, политика паролей) задаются в `.env` на сервере. Роли суперадмина нет.
4. Конфигурацию БД и остальные параметры админ только просматривает.
5. `/admin/import/*` помечаем в контракте как нереализованные.
6. Принудительная смена пароля настраивается в `.env` (`PASSWORD_FORCE_CHANGE`, по умолчанию `admin,instructor`).
7. Массовый импорт из CSV: пароли генерирует сервер и показывает один раз.

Номер ADR — 038: 036 зарезервирован планом ДДС-7, 037 занят диктовкой. Следующая миграция — `00022`, `ExpectedSchemaVersion` сейчас 21 (`internal/platform/postgres/postgres.go:32`).

## Общие правила для всех коммитов

- Каждое изменение состояния, включая экспорт аудита, записывается в аудит в той же транзакции (`audit.Record`).
- В новых ответах нет ФИО курсантов, текстов, транскриптов и секретов. Статистика анонимна, журнал показывает логин действующего лица.
- `openapi.yaml` обновляется в том же коммите, что и поведение. Затем `python3 design-docs/contracts/check.py`.
- Проверки: backend — `make verify`, клиент — `make verify-web` и `npm run lint`, миграции и воркер — `make test-integration`, пользовательский поток — e2e.

## Коммиты

**c0 — docs: ADR-038 и план среза**
- `design-docs/adr/038-admin-role.md`: толкования 1–7 выше, границы (никакого docker socket, никакой правки `.env` из веба, никакой телефонии) и что закрывает каждый пункт ТЗ.
- `slice-dds-10-part2-plan.md`: этот план. Строка о части 2 в `slice-planning-dds.md` (ДДС-10).
- `openapi.yaml`: `/admin/import/classifier|tickets` получают `x-status: not-implemented` и описание «импорт — `emsim import`, ждёт ДДС-5».

**c1 — backend: просмотр журнала аудита**
- `internal/platform/audit/query.go`: `Query(ctx, Filter{From, To, ActorID, ActionPrefix, Outcome, ResourceType}, Cursor{BeforeID}, limit)`. Keyset-пагинация по `id DESC`, используются существующие индексы `audit_log_actor_idx` и BRIN по `at`.
- `internal/platform/audit/http.go`: `GET /admin/audit` (JSON, `next_cursor`) и `GET /admin/audit.csv` (потоковая выгрузка, ограничение по периоду). Экспорт пишется в аудит как `admin.audit.export`.
- Логины действующих лиц берутся через порт потребителя `ActorNames(ctx, ids) map[uuid]string`, реализованный `auth.Service` в `cmd/emsim/api.go`. Прямого чтения `users` из platform нет.
- Неудачный вход по существующему логину начинает писать `ResourceID` = id пользователя (`auth.Service.auditRejectedLogin`), чтобы было видно, чью учётку подбирают.
- Тесты: unit на фильтры и cursor, integration на пагинацию, CSV и запрет не-админу.

**c2 — web: экран «Журнал»**
- `web/src/routes/admin/Audit.tsx`: фильтры (период, пользователь из `/admin/users`, действие, результат), таблица, «ещё», ссылка на CSV.
- Словарь подписей действий в `web/src/adminLabels.ts`, туда же переезжает `kindLabels` из `Status.tsx`. Пункт в `Layout.tsx`, маршрут в `App.tsx`.
- e2e: новый `web/e2e/admin-audit.spec.ts` (вход → запись появилась → фильтр → CSV).

**c3 — backend: уровень логов и версия сборки**
- Переменная `LOG_LEVEL` (debug|info|warn|error, по умолчанию info), разбирается в `internal/platform/config` для api и worker и подключается в `cmd/emsim/api.go`, `worker_admin.go`, `main.go` через `slog.HandlerOptions`. RFC §10 это обещал, но не сделано.
- `var buildVersion` в `cmd/emsim` задаётся через `-ldflags -X` в `Dockerfile` и `Makefile` (git describe). Отдаётся в `AdminStatus.build`. Worker кладёт свою версию в heartbeat, расхождение версий api и worker видно на экране.

**c4 — backend: нагрузка и показатели работы**
- `internal/platform/observability/window.go`: скользящее окно за 5 минут в памяти, заполняется из `InstrumentHTTP`: число запросов, 5xx, p50/p95 по всем запросам и отдельно по `POST /items/{itemId}/actions`.
- В `internal/platform/realtime` появляется счётчик активных SSE-подключений.
- `internal/platform/status/host.go`: `/proc/loadavg`, `/proc/meminfo`, `runtime.NumCPU()`. Вне Linux значения `null`.
- Активные сеансы (`sessions.last_seen_at` за 5 минут) через порт к auth. Идущие занятия и открытые карточки через порт к training (новый `training/postgres` `ActivityCounts`). Worker кладёт загрузку своих пулов в heartbeat.
- `models.stt`: api сам проверяет `STT_URL`, когда `DICTATION=whisper` (переиспользуется `status.HTTPCheck`/`Prober`, `internal/platform/status/probe.go`).
- `AdminStatus.load` в openapi. Тесты: окно (перцентили, вытеснение старых), парсер `/proc` на фикстурах.

**c5 — web: панель «Нагрузка»**
- `Status.tsx`: CPU/load, память, запросы, 5xx, p95 команд, SSE, сеансы, занятия и карточки, пулы воркера, строка STT, версия сборки. Дополняется `web/e2e/admin-status.spec.ts`.

**c6 — backend: конфигурация только для чтения**
- Методы `config.API.Public()` и `config.Worker.Public()` возвращают действующие значения по группам: «База данных» (хост, порт, имя БД, размер пула; без пользователя и пароля), «Производительность», «Модели и диктовка», «Резервное копирование», «Журналирование и аудит», «Безопасность» (TTL сессии, `COOKIE_SECURE`, политика паролей из c12).
- Секреты (ключ LLM, пароль в `DATABASE_URL`) не отдаются никогда.
- Worker публикует снимок своей конфигурации при старте в `platform_heartbeats` (component `config.worker`). Таблица уже есть, миграция не нужна.
- `GET /admin/config`. Тест проверяет, что ни один известный секрет не попадает в ответ.
- В README раздел «Параметры: где и как менять» (.env → `docker compose up -d`).

**c7 — web: экран «Конфигурация»**
- `web/src/routes/admin/Config.tsx`: сгруппированная таблица «параметр / значение / переменная .env / процесс» и подсказка, как изменить. Без полей ввода.

**c8 — backend: режим обслуживания**
- Миграция `00022_platform_maintenance_mode.sql`: однострочная таблица `platform_maintenance(on, reason, set_by, set_at)`. `ExpectedSchemaVersion` = 22, обновить `schema.sql`.
- Пакет `internal/platform/maintenance`: `Get`, `SetTx` (строка `FOR UPDATE` + аудит `admin.maintenance.set`).
- `PUT /admin/maintenance {on, reason}` и `GET /api/v1/system` (все роли, `{maintenance}`).
- `training.Service.Start` и `StartPreview` (`internal/training/service.go:513`, `preview.go:28`) внутри своей транзакции читают строку `FOR SHARE` через порт `MaintenanceGate`. Если режим включён — `409 maintenance_mode`, новый код в закрытом списке ошибок openapi. Идущие занятия и команды курсантов не затрагиваются.
- Integration-тест: включили режим → старт отклонён, идущее занятие работает → выключили → старт проходит.

**c9 — web: режим обслуживания**
- Переключатель с причиной на «Состоянии». Баннер в `Layout.tsx` для всех ролей. В `Lessons.tsx` и в предпросмотре редактора кнопка старта неактивна и объясняет почему. e2e.

**c10 — backend: отчёт о сбоях**
- `GET /admin/failures?from&to` и `.csv` (platform/status):
  - упавшие и `dead_letter` задачи по kind и коду ошибки (количество, первая и последняя);
  - перезапуски сервера (`training.recover` в аудите);
  - отклонённые входы по причинам;
  - записи аудита с `outcome=error`;
  - 5xx из окна c4 с пометкой «с момента запуска api».
- Integration-тест на период и группировку.

**c11 — backend: статистика использования**
- `internal/reporting`: `UsageStats(from, to)` — ряды по дням и итоги:
  - входы и уникальные активные пользователи по ролям;
  - занятия начатые и завершённые по `exercise_type`, превью исключаются;
  - закрытые карточки;
  - оценки auto и expert;
  - ответы ИИ-заявителя и вызовы судьи (по `tasks`).
- Без имён и без содержимого. Входы берутся из аудита через порт к `platform/audit`.
- `GET /admin/usage` — маршрут `GroupAdmin` в `internal/reporting/http`, отдельно от закрытых админу отчётов преподавателя. CSV по той же схеме.

**c12 — web: экран «Отчёты»**
- `web/src/routes/admin/Reports.tsx` с вкладками «Использование» (итоги и столбчатый график по дням, по правилам skill `dataviz`) и «Сбои», общий выбор периода, CSV. e2e на открытие и выгрузку.

**c13 — backend: контроль целостности**
- Задача `integrity.check` (пул report, по расписанию раз в сутки через `maintenanceSchedules` в `cmd/emsim/worker_composition.go`) и `POST /admin/integrity`. Проверки:
  - `blobs`: файл есть, размер и sha256 совпадают;
  - `evidence.digest`: пересчёт через порт training, экспортируется существующий `canonicalDigest` из `internal/training/evidence.go:198`;
  - `scenario_versions.digest`: пересчёт через `content.BodyDigest`;
  - копии бэкапа: новая `backup.Verify(copy)` по `Manifest.Files`;
  - версия схемы.
- Итог — счётчики и до 20 id расхождений, без содержимого. Пишется в `platform_heartbeats` (`integrity`) и в аудит (`integrity.check`, `outcome=error` при расхождении). `AdminStatus.integrity`.
- Integration-тест: испорченный blob и испорченный digest находятся.

**c14 — web: панель «Целостность»**
- На «Состоянии»: дата последней проверки, результат по разделам, id расхождений, кнопка «Проверить сейчас».

**c15 — backend: политика входа и сеансы**
- Миграция `00023_auth_login_policy.sql`: `users.failed_logins`, `locked_until`, `must_change_password`.
- Переменные `.env`: `LOGIN_LOCKOUT_ATTEMPTS` (10), `LOGIN_LOCKOUT_DURATION` (15m), `PASSWORD_MIN_LENGTH` (текущий минимум из `internal/auth/password.go`), `PASSWORD_FORCE_CHANGE` (`admin,instructor`).
- `auth.Service.Login`: блокировка → `423 account_locked` и аудит. Неудача увеличивает счётчик, успех его сбрасывает. Существующий `LoginLimiter` остаётся.
- Создание и сброс пароля админом ставят `must_change_password` для ролей из `PASSWORD_FORCE_CHANGE`.
- `Me.must_change_password`. `POST /me/password {current, new}` снимает флаг и отзывает остальные сеансы. `SessionMiddleware` при флаге отдаёт `403 password_change_required` на всё, кроме `/me`, `/me/password`, `/auth/logout`.
- Админу: `User.locked_until`, `PATCH /admin/users/{id} {unlock: true}`, `GET /admin/users/{id}/sessions` (создан, последняя активность, РМ, истекает), `DELETE /admin/users/{id}/sessions`. Всё с аудитом.
- Тесты: unit по доменным правилам, integration по блокировке и принудительной смене пароля.

**c16 — web: политика входа**
- Экран смены пароля после входа, если стоит флаг (`Login.tsx`, `RequireAuth`).
- `Users.tsx`: значок блокировки, «Разблокировать», список сеансов, «Завершить все сеансы».
- e2e: неудачные попытки → блокировка → разблокировка админом; первый вход инструктора → смена пароля.

**c17 — массовое создание пользователей (backend + web)**
- `POST /admin/users/import?dry_run=` (CSV: `login, full_name, role, service_code`). Всё или ничего в одной транзакции, 422 с номерами строк. Пароли генерирует сервер (`crypto/rand`), возвращает их один раз в ответе, нигде не хранит и не логирует. Аудит — одна запись `admin.user.import` с количеством.
- UI в `Users.tsx`: загрузка, предпросмотр dry-run, создание, скачивание листа «логин — пароль» (генерируется на клиенте из ответа). Integration-тест и e2e.

**c18 — обновление и восстановление с обязательной копией**
- `scripts/update.sh [bundle.tar]`: `docker load` (если передан пакет) → `emsim backup` (при ошибке остановка) → `stop api worker` → `migrate` → `up -d` → ожидание `/readyz`.
- `scripts/restore.sh`: перед заменой делает страховочную копию текущего состояния.
- `make release-bundle`: `docker save` уже собранного образа `emsim` и образов, которые уже есть локально, плюс compose-файлы и скрипты. Новые образы не скачиваются; веса моделей в пакет не входят, это по-прежнему техдолг офлайн-пакета.
- `audit.prune` удаляет строки, только если за последние 24 часа есть успешная копия (при настроенном бэкапе); иначе retryable-ошибка. Так закрывается «удаление без резервного копирования».
- README, раздел «Обновление». Ручная проверка на локальном compose.

**c19 — docs: итог**
- Одна запись в `LOG.MD`. Статус в `slice-planning-dds.md`. Строка ДДС-10 в `CLAUDE.md` (Current delivery state). `web/README.md`.

## Проверка всего среза

- `make verify`, `make verify-web`, `cd web && npm run lint`, `python3 design-docs/contracts/check.py`, `make test-integration` (миграции 00022/00023, задачи, воркер), `cd web && npm run test:e2e` (admin-audit, admin-status, политика входа, импорт).
- Вручную через preview в браузере:
  1. Админ включает обслуживание → инструктор не может стартовать занятие → выключает.
  2. Журнал показывает эти действия, CSV выгружается.
  3. «Состояние» показывает нагрузку, STT, версию, целостность.
  4. Испорченный blob виден после «Проверить сейчас».
  5. «Конфигурация» не показывает секретов.
- Регрессия: у админа по-прежнему `403` на `/lessons`, `/scenarios`, `/items/*/assessment`, `/lessons/*/report`.
