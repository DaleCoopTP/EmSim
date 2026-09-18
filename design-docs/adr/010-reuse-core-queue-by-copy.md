# ADR-010. Очередь из orchestration-core переносится копированием

Статус: accepted · 2026-09-17 · уточнён ADR-016 от 2026-09-18 · основание: `../docs/technical-discovery.md`

## Контекст
В `orchestration-core` есть проверенная тестами durable-очередь на PostgreSQL (claim SKIP LOCKED, lease/heartbeat, fencing по `(worker, token)`, retry с backoff, dead-letter, reaper). Весь код в `internal/` — импортировать как зависимость нельзя; таблица `tasks` связана FK с `runs/run_items` и ограничена `kind IN ('dialogue','judge')`.

## Решение
Скопировать в `internal/platform/tasks`: `queue.go`, `worker/runner.go`, `worker/roles.go` (Composite), `recovery/*`, `postgres/task_queue.go`, `postgres/task_recovery.go`, интеграционные тесты очереди. Адаптировать:
- `Kind` — строка из реестра `kinds.go` (kind → max_attempts, lease, retry base, handler);
- `Lease`/таблица — `scope_type, scope_id, dedup_key UNIQUE, payload jsonb, result jsonb` вместо `run_id/run_item_id/dialogue_id`; FK на домен нет;
- убрать `failItem`: провал задачи меняет только статус её результата;
- lease per-kind (генерация — 10 мин);
- waiting dependencies отдельно от попыток: coordinator активирует готовую задачу до claim;
- эффект результата, audit, NOTIFY и done — общая транзакция с проверкой worker/token/lease; уникальный source_task_id или доменный ключ результата;
- независимый резерв коротких задач; LLM concurrency выбирается замерами;
- administrative retry сохраняет id/dedup/token; оценки с auto/expert и done/cancelled не повторяются. Ошибка подготовки без input возвращается в waiting; sealed input — в pending;
- coordinator — короткий polling-цикл worker каждые 2 с; удерживает STT tasks.result до seal/отмены зависимой оценки;
- priority=100 оценки, 50 генерация, 10 советы. Финальный отказ оценивания и reaper используют общий доменный finalizer; expert отменяет незавершённую evaluate;
- в шапке каждого файла — ссылка на исходный коммит core.

Также берём: admin-эндпоинты, `InstrumentHTTP`, `postgres.go` + goose embed + `cmd/migrate`, Dockerfile/Makefile/CI-скелет, паттерны HTTP-ошибок и идемпотентности.

## Варианты
- Вынести core в библиотеку (`pkg/`) и импортировать — нужно менять core, тянуть его зависимости и домен; отвергнуто.
- Написать очередь заново — неделя и новые баги в гонках; отвергнуто.
- Готовая библиотека (river, gocraft/work) — river хорош, но тянет свою схему и стиль; своя копия уже покрыта нужными тестами; отвергнуто.

## Последствия
- Расхождение с core со временем — принимается; core не развивается под нас.
- ~1,4 k строк кода + ~450 строк тестов, правки ~25 %.
- Доменные пакеты core (run/dialogue/judge/finalization/profiles/inference-протокол) не используются.
