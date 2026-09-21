# EmSim

Тренажёр диспетчера ДДС/112: одна Go-программа `emsim`
(`migrate | api | worker | bootstrap-admin | import`), PostgreSQL как БД и очередь
задач, веб-фронтенд поверх (см. `design-docs/`). Архитектурные решения —
[`design-docs/rfc-001-emsim.md`](design-docs/rfc-001-emsim.md) и
[`design-docs/adr/`](design-docs/adr); что и почему перенесено из
`orchestration-core` — [`docs/technical-discovery.md`](docs/technical-discovery.md).

## Локальный запуск (demo)

```bash
cp .env.example .env   # при желании поменять логин/пароль администратора
docker compose up --build
```

Поднимает `postgres`, применяет миграции (`migrate`), затем одноразовым
сервисом `bootstrap` создаёт начальную учётную запись администратора
(`BOOTSTRAP_ADMIN_LOGIN`/`BOOTSTRAP_ADMIN_PASSWORD` из `.env`, по умолчанию
`admin` / `local-only-admin-password` — сменить перед чем-либо, кроме
локальной демонстрации), затем одноразовым сервисом `seed` загружает
пилотный каталог служб/классификатора/сценариев и подготовленных голосовых
реплик (`seed/`, включая `pilot-phone-01`) от имени этого администратора, запускает `api` (`:8080` — публичный
API, `:8081` — `/healthz`/`/readyz`/`/metrics`) и один `worker --role=all`
(`:8082` — его собственные `/healthz`/`/readyz`/`/metrics`). `bootstrap` и
`seed` идемпотентны: повторный `docker compose up` не создаёт второго
администратора и не дублирует уже загруженный каталог (`make seed`
перезапускает только сервис `seed`, без остального стека — например, после
добавления файла в `seed/scenarios/`). LLM/STT/Caddy добавятся вместе с
клиентами инференса.

`blob-data` — общий Docker volume seed/API/worker для content-addressed
голосовых файлов и записей докладов. Не удаляйте его при обновлении, если
нужно сохранить уже принятые записи.

Демо-профиль не ставит перед `api` reverse proxy с TLS, поэтому `api`
запускается с `COOKIE_SECURE=false` — иначе браузер не отправлял бы cookie
сессии обратно по обычному HTTP (ADR-008).

`docker compose up --build` собирает и веб-клиент (`web/`, стадия
`node:22-alpine` в `Dockerfile`) и встраивает его в бинарник `emsim`
(`web/embed.go`, `//go:embed`) — `api` сам раздаёт SPA на всех путях вне
`/api/`, отдельный веб-сервер не нужен (ADR-009).

Дальше — в браузере на `http://localhost:8080`: вход администратором →
«Рабочие места» (добавить РМ, сохранить) → «Пользователи» (создать
преподавателя и обучаемого со службой из уже загруженного каталога) →
«Выйти» → в другом браузере/профиле вход преподавателя → «Сценарии» —
пилотный каталог (два прохождения карточки «Дерево во дворе», одно с
намеренной ошибкой в округе — эталон, список оповещения и версии видны
без JSON) → «Выйти» → вход обучаемого с номером РМ → ФИО, служба, РМ и
«Ожидайте назначения занятия»; F5 сохраняет вход, «Выйти» отзывает сессию.

Обновление уже развёрнутой установки (новая миграция добавляет каталог
служб как внешний ключ у `users.service_code`) и диагностика конфликтов при
повторной загрузке каталога — [`seed/README.md`](seed/README.md).

То же через API — curl-пример (`service_code` — код из уже загруженного
каталога, `GET /api/v1/services`; неизвестный код `admin/users` отклоняет
с `422 validation_failed`, срез 2, C5):

```bash
curl -c cookies.txt -s -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"login":"admin","password":"local-only-admin-password"}'

curl -b cookies.txt -s -X PUT http://localhost:8080/api/v1/admin/workstations \
  -H 'Content-Type: application/json' \
  -d '[{"number":5,"label":"РМ-05"}]'

curl -b cookies.txt -s -X POST http://localhost:8080/api/v1/admin/users \
  -H 'Content-Type: application/json' \
  -d '{"login":"trainee-05","password":"correct-horse-battery-staple","full_name":"Иванов Иван","role":"trainee","service_code":"dds_district"}'
```

## Разработка

```bash
make verify              # gofmt, build, go vet, go test, staticcheck (не требует Node)
make test-integration    # очередь/recovery + запуск и crash recovery реального worker (PostgreSQL 16)
make compose-config      # проверить compose.yaml без сборки образов
make seed                # перезапустить только одноразовый сервис seed поверх уже поднятого стека
make verify-web          # web/: npm ci, регенерация типов из openapi.yaml, tsc, vite build
python3 design-docs/contracts/check.py   # офлайн-проверка контрактов (openapi.yaml, *.schema.json, примеры)
cd web && npm run lint   # oxlint
```

`go build`/`make verify` не требуют Node: `web/dist` несёт закоммиченный
`.gitkeep`-плейсхолдер, так что `//go:embed all:dist` компилируется и без
собранного SPA — `api` в этом случае отвечает на любой путь вне `/api/`
тем же закрытым конвертом ошибок с понятным сообщением про
`make web-build`. Разработка самого клиента — см. [`web/README.md`](web/README.md)
(`npm run dev` на `:5173` с прокси `/api` на запущенный отдельно `emsim api`).

`make test-integration` использует Docker (testcontainers). Без Docker —
поднять локальный PostgreSQL 16 и передать `TEST_DATABASE_URL` (отдельная
пустая тестовая БД, схема на время теста пересоздаётся):

```bash
TEST_DATABASE_URL="postgres://user@localhost:5432/emsim_test?sslmode=disable" \
  go test -tags=integration -count=1 ./test/integration/...
```


Операции очереди используют часы PostgreSQL; проверка lease выполняется после
получения блокировки строки. Для атомарной записи доменного эффекта используйте
`EnqueueTx`, `CancelTx` и `Terminal` с общей `pgx.Tx`: вызывающий код отвечает
за commit/rollback. Повтор `Terminal` сравнивает статус, код ошибки и JSONB-результат
(порядок ключей несущественен); изменение результата возвращает конфликт.

Интеграционный process-тест собирает `cmd/emsim`, проверяет реальные роли
`worker/maintenance/all`, graceful shutdown и SIGKILL во время фиксации noop.
Для ускорения теста expiry оставшегося lease меняется в тестовой БД; reaping и
повторное выполнение выполняют настоящие процессы приложения.

Второй process-тест (`test/integration/api_process_test.go`) собирает тот же
бинарник и проходит срез 1 целиком через настоящий HTTP: `migrate up` →
`bootstrap-admin` (дважды — второй запуск должен остаться no-op) → `api` →
вход администратора → создание РМ/преподавателя/обучаемого → выход
администратора и отказ по старой cookie → вход обучаемого с РМ → `GET /me`
после «перезагрузки страницы» → 403 у обучаемого на административный API →
выход обучаемого → отказ по неверному паролю.

Тот же файл добавляет сквозной каталожный тест среза 2: `migrate up` →
`bootstrap-admin` → `emsim import seed` → `api` — преподаватель видит
каталог и просмотр карточки без утечки закрытых полей (`pilot_goal` не
покидает `reference`), администратор доходит до `/services`, но получает 403
на `/scenarios`, неаутентифицированный запрос — 401, а создание обучаемого с
неизвестным `service_code` — настоящий 422 от работающего процесса `api`.

Срез 4 (групповые занятия, очереди, hard, монитор, stop, SSE, restart
recovery) проверяется на трёх уровнях: unit — Hub epoch/cursor/resync
(`internal/platform/realtime/hub_test.go`); `training.Service` напрямую
против настоящего PostgreSQL (`test/integration/training_service_test.go`) —
независимые очереди нескольких участников на одной и на разных версиях
сценария, конкурентные команды на одной карточке, hard-очередь (валидация
`spawn_every_s`, ровно одна выдача за tick даже после часового простоя
планировщика — RFC-001 §7.2 «пропущенные интервалы не воспроизводятся
пачкой»), stop как барьер с durable `lesson.close`, восстановление после
рестарта (идемпотентные маркеры interruption, дедлайны и `due_at` карточек/
событий не сдвигаются, просроченное событие доставляется с `late=true`),
`control_report` — реплей, устаревший `expected_seq`, доказанная неизменность
evidence — и живой `RunListener` поверх настоящего `LISTEN`/`NOTIFY`
(`test/integration/realtime_test.go`); и, наконец, HTTP end-to-end через
настоящий процесс `api` (`test/integration/training_realtime_e2e_test.go`,
`TestAPIProcessSSEStreamSnapshotReplayResyncAndFiltering`) — RFC-001 §7.7
целиком: `stream.ready` раньше любого другого события, переподключение по
`Last-Event-ID` и по запасному `?cursor` (для нового `EventSource`
без заголовков) реплеит пропущенное, неизвестный cursor даёт `resync`, а
поток преподавателя и поток каждого обучаемого не видят чужих событий.

Срез 5 добавляет браузерный телефон: `pilot-phone-01` требует до статуса
`accepted` завершить звонок руководителю бригады (4152), сообщив адрес,
тип происшествия и принятые меры. В seed включены записанные greeting/ack
в PCM WAV, mono, 16-bit, 16 kHz. Тест
`TestTrainingPhoneCallRecordingAndEvidenceEndToEnd` проходит звонок,
manifest/upload/replay, закрытие/evidence и доступ к записи преподавателя.

Для ручной приёмки в Chrome, Edge и актуальном Яндекс Браузере разрешите
микрофон на странице рабочего места, завершите звонок и убедитесь, что
состояние записи стало `ready`. Отдельно проверьте отказ в разрешении
(звонок завершается с `recording=null`) и повтор загрузки после временного
разрыва сети; незагруженные байты после перезагрузки вкладки восстановить
нельзя, и после deadline состояние отображается как `missing`.

Срез 6 добавляет разбор оценки преподавателем. При закрытии training-карточки
worker сначала запечатывает `assessment_inputs`, затем создаёт одну
детерминированную `auto`-ревизию. Пока LLM-проверки не подключены, такая
ревизия имеет `needs_review` и пустой итоговый балл — это ожидаемое состояние,
а не ошибка очереди. Преподаватель открывает завершённое или остановленное
занятие → «Разбор», видит доказательства правил, журнал и записи звонков, и
сохраняет экспертную ревизию как итоговую. Для этого в Compose должен работать
worker с `LLM_CONCURRENCY>=1` (стандартный `.env.example` уже задаёт это).

Приёмочный тест `TestAssessmentPipelineThroughAPIAndWorker` проходит реальный
API и отдельный процесс worker: закрытие → auto `needs_review` → список
разбора → экспертная ревизия. Дополнительно `assessment_pipeline_test.go`
проверяет finalizer, потерю lease, ручную оценку без auto и гонки ревизий.
