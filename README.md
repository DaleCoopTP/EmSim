# EmSim

Тренажёр диспетчера ДДС/112: одна Go-программа `emsim`
(`migrate | api | worker | bootstrap-admin`), PostgreSQL как БД и очередь
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
локальной демонстрации), запускает `api` (`:8080` — публичный API, `:8081` —
`/healthz`/`/readyz`/`/metrics`) и один `worker --role=all` (`:8082` — его
собственные `/healthz`/`/readyz`/`/metrics`). `bootstrap` идемпотентен:
повторный `docker compose up` не создаёт второго администратора. LLM/STT/Caddy
добавятся вместе с клиентами инференса.

Демо-профиль не ставит перед `api` reverse proxy с TLS, поэтому `api`
запускается с `COOKIE_SECURE=false` — иначе браузер не отправлял бы cookie
сессии обратно по обычному HTTP (ADR-008).

Вход администратора и создание рабочего места и обучаемого — curl-пример:

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
make verify              # gofmt, build, go vet, go test, staticcheck
make test-integration    # очередь/recovery + запуск и crash recovery реального worker (PostgreSQL 16)
make compose-config      # проверить compose.yaml без сборки образов
```

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
