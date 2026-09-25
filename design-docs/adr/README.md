# ADR — архитектурные решения EmSim

Формат: контекст → решение → варианты → последствия. Один файл — одно решение. Изменение решения — новый ADR со ссылкой «supersedes».

| ADR | Решение | Статус |
|---|---|---|
| [001](001-modular-monolith-single-binary.md) | Модульный монолит на Go, один бинарник с ролями | accepted |
| [002](002-postgres-as-queue-and-bus.md) | PostgreSQL как БД, очередь задач и шина уведомлений; файлы — на диске | accepted |
| [003](003-llm-outside-interactive-path.md) | Модели только до занятия и после карточки | accepted |
| [004](004-server-source-of-truth-command-protocol.md) | Сервер — источник правды; идемпотентный командный протокол | accepted |
| [005](005-phone-simulator-without-sip.md) | Телефон — симулятор в браузере без SIP | accepted |
| [006](006-evidence-snapshot-and-assessment-revisions.md) | Оценка по неизменяемому snapshot; ревизии; преподаватель важнее ИИ | accepted |
| [007](007-sse-for-push.md) | SSE для push в браузер | accepted |
| [008](008-cookie-sessions-no-mfa.md) | Cookie-сессии, argon2id, три роли, без MFA | accepted |
| [009](009-frontend-react-copy-arm112.md) | React + TypeScript, копия АРМ-112 без UI-кита | accepted |
| [010](010-reuse-core-queue-by-copy.md) | Очередь из orchestration-core переносится копированием | accepted |
| [011](011-scenario-events-timeline.md) | События сценария по таймлайну как источник «жизни» карточки | accepted |
| [012](012-deterministic-level-recommendation.md) | Рекомендация уровня — детерминированная политика, не модель | accepted |
| [013](013-rubric-default-plus-case-scoring.md) | Рубрика по умолчанию + поправки в эталоне кейса; два оценщика | accepted |
| [014](014-accepted-design-review-corrections.md) | Согласованные уточнения ревью: одна автооценка, evidence/STT, команды, очередь, восстановление, timing и основания отчётов | accepted |
| [015](015-dds-first-operator112-extension.md) | Сначала ДДС; тип упражнения, граница правил процесса и отдельная рубрика будущего оператора 112 | accepted |
| [016](016-review-fixes-and-mvp-simplifications.md) | Исправления A1–A10 и упрощения B1–B5: interruption, единый input, ручная оценка без auto, localStorage, очередь и отдельное состояние assessment | accepted |
| [017](017-slice3-pilot-close-and-field-correction.md) | Срез 3: пилотное `close` после `accepted` по `pilot_goal`, команда `set_card_field` для исправления округа | accepted |
| [018](018-slice4-queues-events-stop.md) | Очереди, события, stop и монитор среза 4 | accepted |
| [019](019-slice6-deterministic-assessment.md) | Срез 6: детерминированная авто-оценка, finalizer и экспертные ревизии | accepted |
| [020](020-slice7-reporting-snapshots.md) | Срез 7: отчётные проекции, CSV и неизменяемые PDF-снимки | accepted |
| [021](021-operator112-first-intake-slice.md) | Первый входящий вызов 112, отдельное состояние и ручная рубрика | accepted |
| [022](022-operator112-caller-simulator.md) | Контракт симулятора заявителя для 112 | accepted |
| [023](023-operator112-notify-and-save.md) | Оповещение служб «Сохранить → оповестить и сохранить карточку» | accepted |
| [024](024-operator112-async-caller-reply.md) | Асинхронный ход свободного диалога с заявителем | accepted |
| [025](025-operator112-ai-caller.md) | ИИ-заявитель 112 на интерактивном пути, уточняет ADR-003 | accepted |
