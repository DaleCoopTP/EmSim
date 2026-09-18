# EmSim

Тренажёр диспетчера ДДС/112: одна Go-программа `emsim` (`migrate | api | worker`),
PostgreSQL как БД и очередь задач, веб-фронтенд поверх (см. `design-docs/`).
Архитектурные решения — [`design-docs/rfc-001-emsim.md`](design-docs/rfc-001-emsim.md)
и [`design-docs/adr/`](design-docs/adr); что и почему перенесено из
`orchestration-core` — [`docs/technical-discovery.md`](docs/technical-discovery.md).

## Локальный запуск (demo)

```bash
docker compose up --build
```

Поднимает `postgres`, применяет миграции (`migrate`), запускает `api`
(`:8080` — публичный API, `:8081` — `/healthz`/`/readyz`/`/metrics`) и один
`worker --role=all` (`:8082` — его собственные `/healthz`/`/readyz`/`/metrics`).
LLM/STT/Caddy добавятся вместе с клиентами инференса.

## Разработка

```bash
make verify              # gofmt, build, go vet, go test, staticcheck
make test-integration    # тесты очереди/recovery против PostgreSQL 16 (testcontainers)
make compose-config      # проверить compose.yaml без сборки образов
```

`make test-integration` использует Docker (testcontainers). Без Docker —
поднять локальный PostgreSQL 16 и передать `TEST_DATABASE_URL` (отдельная
пустая тестовая БД, схема на время теста пересоздаётся):

```bash
TEST_DATABASE_URL="postgres://user@localhost:5432/emsim_test?sslmode=disable" \
  go test -tags=integration -count=1 ./test/integration/...
```
