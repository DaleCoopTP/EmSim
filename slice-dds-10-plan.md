# План среза ДДС-10 (часть 1): доступ из класса, бэкап, «Состояние», аудит, удалённая модель, демо-стенд

Дата: 2026-09-28. Статус: реализован (коммиты c1–c10 в ветке `dds-10`); браузерный e2e `admin-status.spec.ts` и ручной `make demo` на итоговом образе не прогнаны — см. `LOG.MD`. Решение и обоснование — [ADR-033](design-docs/adr/033-delivery-class-profile.md).

## Контекст

ДДС-4, ДДС-5 и ДДС-8 ждут от заказчика структуру протокола классификации. Поэтому на время ожидания берём то, что от неё не зависит: пункты 1–6 ДДС-10. Они общие для ДДС и 112 и нужны к сдаче в любом случае.

Что показала сверка с кодом:
- **Доступ из класса.** Порты api и worker опубликованы только на `127.0.0.1` (`compose.yaml:108-110,166`), `COOKIE_SECURE=false`, Caddy нет. С других ПК класса к тренажёру не подключиться.
- **Эндпоинты только в контракте.** `/admin/status`, `/admin/backup` и `/admin/tasks/{id}/retry` описаны в `openapi.yaml:136-156,620`, но не реализованы. В `internal/auth/http/admin.go` есть только пользователи и рабочие места.
- **Нет инфраструктуры для периодических задач:**
  - нет планировщика задач по времени;
  - нет очистки аудита;
  - `audit_log` индексирован без отдельного индекса по `at`;
  - в runtime-образе нет `pg_dump`.
- **Удалённая модель.** `internal/platform/llm/client.go` не умеет передавать API-ключ.
- **Демо-данные.** В `seed/` нет пользователей и рабочих мест: e2e заводит их через `POST /admin/users`.

Решения пользователя от 28.09:
- бэкап в каталог хоста, хранить 14 копий;
- TLS от внутреннего CA Caddy (`tls internal`);
- удалённая модель — OpenAI-совместимый API с `Authorization: Bearer`;
- пункты 7 (нагрузка, W0) и 8 (офлайн-пакет, документация, видео) — в техдолг.

Импорт через админку (`/admin/import/*`) тоже уходит в техдолг: сейчас импорт работает только из CLI.

## Ключевые проектные решения (ADR-033)

- **Профиль класса — отдельный overlay `compose.class.yaml`.**
  - Добавляет сервис `caddy` (образ закреплён по digest): `tls internal` с `skip_install_trust`, `reverse_proxy api:8080`, редирект с 80 на 443.
  - Для api: `COOKIE_SECURE=true`, `ports: !reset []`. Наружу опубликован только Caddy.
  - Базовый `compose.yaml`, e2e и CI не меняются: там по-прежнему http на localhost.
  - Корневой сертификат выгружается `make class-ca` из тома `caddy-data`; его ставят на ПК класса.
  - Имя или IP сервера задаётся `EMSIM_HOST`.
  - `RequireSameOrigin` сверяет Host, а Caddy передаёт Host по умолчанию, поэтому менять ничего не нужно. SSE Caddy отдаёт без буферизации.
- **Планировщик.** Новый `tasks.Scheduler` (Supervisor) в роли `maintenance`, рядом с reaper и sampler в `composeMaintenance` (`cmd/emsim/worker_composition.go`).
  - Тикает раз в минуту. Когда наступает время слота, ставит задачу с `DedupKey = "<kind>:daily:<YYYY-MM-DD>"`. Благодаря `ON CONFLICT DO NOTHING` рестарт процесса не создаёт дублей.
  - Время задаётся в `SCHEDULE_TZ` (по умолчанию `Europe/Moscow`, база часовых поясов встраивается через `time/tzdata`).
  - Ошибка тика логируется, как у sampler, и процесс не падает.
- **`audit.prune`.**
  - Пул `short`. Удаляет пачками по 5000 строк, каждая пачка в своей транзакции; затем `Terminal` задачи.
  - Возраст хранения: `AUDIT_RETENTION_DAYS=183`.
  - Сама очистка записывается в аудит (`actor_role: system`, число удалённых строк).
  - Миграция `00020`: BRIN-индекс `audit_log (at)`.
- **`backup.run`.**
  - Пул `report` (1 слот): бэкап долгий и не должен занимать короткий пул `lesson.close`.
  - Внутри: `pg_dump -Fc` плюс `tar.gz` блобов без `.staging`, плюс `manifest.json` (sha256 и размеры файлов, версия схемы, время).
  - Пишет во временный `.tmp-*`, потом атомарно переименовывает в `emsim-YYYYMMDD-HHMMSS/`. Хранятся последние `BACKUP_KEEP=14` успешных копий.
  - Пароль БД передаётся в `pg_dump` через `PG*`-переменные окружения, а не аргументом.
  - Каталог хоста монтируется из `BACKUP_HOST_DIR` (по умолчанию `./backups`). Одноразовый сервис `backup-dir` с `user: root` делает `chown 65532` на этот каталог.
  - Расписание — `BACKUP_AT=03:00`. Ручной запуск — `POST /admin/backup` (`DedupKey backup.run:manual:<uuid>`).
  - Восстановление: `scripts/restore.sh <каталог>` (сверка manifest, `pg_restore --clean`, распаковка блобов при остановленных api и worker) и раздел README.
- **Отметки живости компонентов.** Миграция `00020` добавляет таблицу `platform_heartbeats (component PK, status, detail jsonb, checked_at)`. Из неё api узнаёт то, что видит только worker:
  - api не подключён к сети `inference`, поэтому доступность модели проверяет worker: раз в 30 с запрашивает `/health` и пишет результат;
  - свободное место в каталоге бэкапов.
- **`GET /admin/status`** (пакет `internal/platform/status`, узкий порт чтения, подключается в `cmd/emsim/api.go` через `authhttp.SessionMiddleware` + `RequireRole(auth.GroupAdmin)`). Показывает:
  - версию схемы (goose);
  - задачи по видам и статусам (тот же запрос, что `tasks/pgsampler.go`);
  - модели из heartbeat worker (`stt`/`tts` = `null`);
  - свободное место под блобами (`Statfs(BLOB_ROOT)`) и под бэкапами;
  - `last_backup_at` и последние копии: время, размер, статус;
  - список `failed`/`dead_letter`: id, вид, код ошибки, время, без содержимого.

  Контракт расширяется полями `workers`, `backups`, `failed_tasks`.
- **`POST /admin/tasks/{id}/retry`.**
  - Общий `tasks.Store.Retry`: только для `failed`/`dead_letter`. Бюджет попыток увеличивается, id, dedup и токен сохраняются, задача возвращается в `pending`.
  - У вида задачи может быть собственная проверка (`RetryGuard`). У `assessment.evaluate` она запрещает повтор, если уже есть auto или expert, и возвращает задачу в `waiting`, если входа нет (как в openapi). `caller.reply` повторять запрещено: ход уже закрыт finalizer'ом.
  - Доступ: `GroupAdmin`. Заглушка `GroupTasks` снимается, решение записывается в ADR.
- **Удалённая модель.**
  - `llm.Client.APIKey` → заголовок `Authorization: Bearer`. Ключ не попадает в логи и ошибки.
  - Переменные: `CALLER_LLM_API_KEY` и `JUDGE_LLM_API_KEY`, с запасным общим `LLM_API_KEY`.
  - `LLM_DIALECT=llama|openai`: в режиме `openai` не отправляется специфичный для llama.cpp `repeat_penalty`.
  - Overlay `compose.remote-llm.yaml` отключает сервис `llm`.
  - `bench-llm` работает без `/metrics` и `timings`.
  - ADR фиксирует: при удалённой модели реплики диалога и описания уходят за периметр. Режим включается только явно.
- **Демо-стенд.**
  - Команда `emsim demo-setup` работает через `auth.Service`, идемпотентно. Создаёт:
    - рабочие места `РМ-01…РМ-N`;
    - преподавателя;
    - обучаемых ДДС района, 03 и 112.
  - Пароль берётся из `DEMO_PASSWORD` и обязателен (значения по умолчанию нет).
  - Запуск — сервис compose в профиле `demo` и `make demo`. Сценарий показа описывается в README.

## Коммиты

**c1 — `docs: DDS-10 part 1 plan and ADR-033 (delivery)`**
- Новые файлы:
  - `design-docs/adr/033-delivery-class-profile.md` (решения выше);
  - `slice-dds-10-plan.md` (этот план).
- Обновить:
  - `design-docs/adr/README.md`;
  - `slice-planning-dds.md`: ДДС-10 сужается до пунктов 1–6, в строку «Техдолг» добавляются нагрузочный прогон и W0 (7), офлайн-пакет, документация и видео (8), `/admin/import/*`;
  - `CLAUDE.md` («Current delivery state»);
  - `LOG.MD`.
- Проверка: `python3 design-docs/contracts/check.py`.

**c2 — `deploy: Caddy class profile with internal TLS`**
- Новые файлы:
  - `compose.class.yaml`;
  - `deploy/caddy/Caddyfile`.
- Обновить:
  - `.env.example`: `EMSIM_HOST`, `HTTPS_PORT`;
  - `Makefile`: `class-up`, `class-ca`, `compose-config` проверяет и overlay;
  - README: раздел «Класс: доступ по HTTPS».
- Проверка:
  - `make compose-config`;
  - вручную: стек с overlay, `curl --cacert root.crt https://$EMSIM_HOST/`, при логине cookie с `Secure`, SSE через Caddy во встроенном браузере;
  - api недоступен напрямую с хоста.

**c3 — `llm: bearer API key, openai dialect, remote-model profile`**
- Обновить:
  - `internal/platform/llm/client.go`;
  - `internal/platform/config/worker.go` и тесты к нему;
  - сборка клиентов в `cmd/emsim/worker_composition.go:443,535` и `bench_llm.go:441,452,616`.
- Новый файл: `compose.remote-llm.yaml`.
- Тесты `client_test.go`:
  - заголовок уходит;
  - ключ не попадает в текст ошибки;
  - в режиме `openai` нет `repeat_penalty`.
- Проверка: `go test ./internal/platform/llm/... ./internal/platform/config/... ./cmd/emsim/...`, `make compose-config`.

**c4 — `platform: daily task scheduler and audit.prune`**
- `migrations/00020_platform_maintenance.sql`:
  - BRIN-индекс по `audit_log.at`;
  - таблица `platform_heartbeats`;
  - `design-docs/contracts/schema.sql` в том же коммите;
  - при необходимости — список таблиц readiness в `internal/platform/postgres/postgres.go`.
- Новый файл `internal/platform/tasks/scheduler.go`: `Schedule{Kind, At, DedupPrefix}`, внедряемые часы.
- Обновить:
  - `internal/platform/audit`: `Prune(ctx, pool, before, batch)`;
  - вид задачи и обработчик в `registerKinds`/`composePools`;
  - `composeMaintenance` получает `store`;
  - конфигурация: `SCHEDULE_TZ`, `AUDIT_RETENTION_DAYS`, `AUDIT_PRUNE_AT`.
- Тесты:
  - планировщик: слот ещё не наступил, наступил, повторный тик, рестарт, смена суток;
  - интеграционный: очистка удаляет только старые строки пачками и пишет свою запись аудита.
- Проверка: `make verify`, `make test-integration`.

**c5 — `backup: backup.run task, host backup dir, restore script`**
- `Dockerfile`: в runtime добавить `postgresql16-client` (major-версия совпадает с `postgres:16`).
- Новый пакет `internal/platform/backup`: запуск `pg_dump`, tar блобов, manifest, ротация, атомарный rename.
- Обновить:
  - вид `backup.run` в пуле `report`;
  - расписание `BACKUP_AT`;
  - конфигурация `BACKUP_DIR`, `BACKUP_KEEP`.
- Compose:
  - `backup-dir` (chown);
  - bind-монтирование `${BACKUP_HOST_DIR:-./backups}:/backups` для worker;
  - `backups/` в `.gitignore`.
- CLI и скрипты:
  - `emsim backup` — ручной запуск, например перед обновлением;
  - `scripts/restore.sh`;
  - README «Резервные копии и восстановление».
- Тесты:
  - юнит: ротация, manifest, недописанный `.tmp` не считается копией;
  - интеграционный: бэкап → восстановление в чистую БД → совпадают счётчики строк ключевых таблиц и sha256 блобов. Пропускается, если на хосте нет `pg_dump`.
- Проверка: `make verify`, `make test-integration`, ручной прогон в compose.

**c6 — `platform: admin status and manual backup endpoints`**
- Новый пакет `internal/platform/status`: store и HTTP-обработчик.
- Проба модели в worker: supervisor в `maintenance`, пишет в `platform_heartbeats`.
- Эндпоинты:
  - `GET /admin/status`;
  - `POST /admin/backup` → `202 {task_id}`, в той же транзакции audit.
- Обновить:
  - `openapi.yaml`: расширенная схема status, коды ответов;
  - `check.py`, если нужно.
- Тесты:
  - обработчик: доступ только админу, состав ответа, в ответе нет содержимого задач;
  - интеграционный: ручной бэкап виден в `backups` и `last_backup_at`.
- Проверка: `make verify`, `python3 design-docs/contracts/check.py`, `make test-integration`.

**c7 — `tasks: admin retry with per-kind guards`**
- Обновить:
  - `tasks.Store.Retry` в `pgstore.go` (lease-токен не сбрасывается и растёт монотонно);
  - `RetryGuard` в `Registry`;
  - guard `assessment.evaluate` в `internal/assessment`;
  - запрет повтора для `caller.reply`;
  - `POST /admin/tasks/{id}/retry`: 200 / 404 / 409 `not_retryable`;
  - `openapi.yaml`: ошибки и тег;
  - `internal/auth/authz.go`: `GroupTasks` убрать или переназначить.
- Тесты (интеграционные):
  - `dead_letter` → `pending` → `done`;
  - `done` и `cancelled` → 409;
  - оценка с auto → 409;
  - оценка без input → `waiting`.
- Проверка: `make verify`, `make test-integration`, `check.py`.

**c8 — `web: admin status screen`**
- Новый экран `web/src/routes/admin/Status.tsx`:
  - БД и схема, задачи, модели, диск, бэкапы с кнопкой «Создать копию сейчас», неудачные задачи с кнопкой «Повторить»;
  - обновление раз в 10 с, предупреждение, если последней удачной копии больше 26 ч.
- Обновить:
  - хуки в `web/src/api/admin.ts`;
  - маршрут в `App.tsx`;
  - пункт «Состояние» в `Layout.tsx`;
  - `npm run generate:api`.
- Проверка: `make verify-web`, `cd web && npm run lint`, ручная проверка во встроенном браузере.

**c9 — `cmd: demo-setup and demo profile`**
- Новый файл `cmd/emsim/demo.go` (`demo-setup`, через `auth.Service`, идемпотентно).
- Обновить:
  - сервис `demo-setup` в compose, профиль `demo`;
  - `make demo`;
  - README «Демо-стенд» со сценарием показа.
- Тест: повторный запуск ничего не дублирует и не меняет пароль существующего пользователя.
- Проверка: `go test ./cmd/emsim/...`, ручной `make demo`.

**c10 — `test+docs: admin status e2e, DDS-10 part 1 complete`**
- Новый e2e `web/e2e/admin-status.spec.ts`:
  - админ открывает «Состояние»;
  - ручной бэкап доходит до «готово» и появляется в списке;
  - визуальный эталон.
- Обновить: README, `slice-planning-dds.md` (статус), `CLAUDE.md`, запись в `LOG.MD`.
- Проверка: `make verify`, `make verify-web`, `make test-integration`, `cd web && npm run test:e2e`, `check.py`.

## Проверка (сквозная)

1. `docker compose -f compose.yaml -f compose.class.yaml up --build`.
2. С другого устройства в сети открыть `https://$EMSIM_HOST` с установленным `emsim-root.crt`. Пройти вход, карточку ДДС и live-монитор; SSE должен работать через Caddy.
3. На экране «Состояние» нажать «Создать копию сейчас». В `./backups/` должен появиться каталог с `db.dump`, `blobs.tar.gz` и `manifest.json`.
4. Проверить `scripts/restore.sh` на копии стенда: данные и записи на месте.
5. Сдвинуть `BACKUP_AT` на ближайшую минуту и убедиться, что ночная копия ставится ровно один раз, в том числе после рестарта worker.
6. Проверить `AUDIT_RETENTION_DAYS=0` на тестовой БД: старые записи аудита удаляются, появляется запись об очистке.
7. Уронить задачу (например, остановить `llm` при оценке 112). Она появляется в неудачных; «Повторить» после запуска модели доводит её до `done`.
8. Проверить `compose.remote-llm.yaml` с внешним OpenAI-совместимым сервером: ИИ-заявитель и судья работают, ключ не виден ни в логах, ни в ответах API.
9. `make demo`: входят преподаватель и обучаемые РМ-01…РМ-N.
