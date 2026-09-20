# Срез 4 — план реализации

Контракт: `slice-planning.md` §5 и ADR-018.

Коммиты C1–C11: контракт; переносимые ссылки событий и seed; миграция событий/отчётов; групповые очереди; scheduler; restart recovery; control report; stop worker; SSE и monitor; UI; сквозная приёмка.

Инварианты: выдача использует только назначенную очередь; `runs.queue_cursor` и `UNIQUE(run_id, ordinal)` защищены run lock; stop cutoff отделяет evidence от позднего журнала; PostgreSQL остаётся источником состояния, SSE только инвалидирует snapshot.
