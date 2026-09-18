# Трассировка требований

`AR-01…26` соответствуют строкам `additional_requirements.md` в исходном порядке. «Покрыто проектом» не означает «реализовано» или «прошло проверку». Все перечисленные тесты — обязательные задачи плана, пока не выполненные тесты продукта.

## Дополнительные требования: 26 из 26

| ID | Требование | Архитектурное решение / владелец | Приёмочный сценарий | Пакет |
|---|---|---|---|---|
| AR-01 | Учёт результатов и прогресса | `Assignment.operator_id`, `TrainingRun`, versioned assessment, reporting skill projections | AT-01: две попытки одного оператора и экспертная ревизия дают правильную историю/прогресс без двойного подсчёта | W3,W7,W8 |
| AR-02 | Аутентификация и роли | IAM, локальный IdP или accounts, sessions, RBAC + resource scope | AT-02: operator/instructor/admin; чужие run/report/SSE/file запрещены; блокировка отзывает сессию | W1 |
| AR-03 | Защита каналов | TLS на всех hop, CA, PostgreSQL verify-full, SIP-TLS/SRTP | AT-03: packet/config inspection; незашифрованные соединения и неизвестный CA отклоняются | W1,W9,W10 |
| AR-04 | Backup ≥1/сутки | Daily DB + blobs + конфигурация, независимый storage внутри LAN, restore job | AT-04: возраст успешной копии ≤24ч, восстановление в чистое окружение, сравнение данных и hashes | W10 |
| AR-05 | Заданные платформы/железо | Browser client, Linux backend, CPU baseline, сменный локальный inference endpoint | AT-05: установка и два режима на минимальных АРМ/сервере из ТЗ; протокол load/quality | W0,W9,W11 |
| AR-06 | Локальная LLM | Local caller/judge/generation; локальные STT/TTS/материалы | AT-06: отключён внешний egress; генерация, диалог, judge и интерфейс продолжают работать | W2,W4,W7,W9 |
| AR-07 | Несколько одновременных пользователей | User-scoped runs, DB state, task pool, ACL и object scopes | AT-07: параллельные пользователи без смешения transcript, карточек, результатов и media | W3,W4,W11 |
| AR-08 | Удалённый контроль | Instructor UI внутри управляемой сети/VPN, live projection, выбранный run SSE | AT-08: преподаватель с другого АРМ видит действия/историю/оценку своей группы и может остановить | W3,W6,W8 |
| AR-09 | UI ≤2с при 100 пользователях | Быстрые команды, precomputed read views, async heavy work, responsive UI | AT-09: end-to-end action→usable committed state ≤2с, max/p95/p99, 100 пользователей; LLM separately measured, loading banner не считается содержательным ответом | W4,W8,W11 |
| AR-10 | ≥20 одновременных сессий | Active run isolation, 20 media slots, reserved inference capacity, отдельные job quotas | AT-10: ≥20 одновременно активных call/card sessions, отдельно 20 voice calls на целевом профиле; не 20 простаивающих HTTP connections | W4,W5,W9,W11 |
| AR-11 | VoIP ≤150мс | Asterisk/media plane, WebRTC/Opus, local SIP, network/jitter budget | AT-11: односторонний mouth-to-ear/capture-to-playback transport на 20 вызовах ≤150мс в определённой LAN; clock sync/loopback fixture | W9,W11 |
| AR-12 | Сбои сети до 30с без потери данных | Durable ACK, browser queue, replay, resume по run/media IDs, audio chunks | AT-12: 5/15/30с обрывы до/после commit, ACK и stop; нет утраченных подтверждённых данных, buffers восстановлены, RTO reconnect ≤30с после возврата сети | W4,W6,W9,W11 |
| AR-13 | БД ≥100 операций/с | pgxpool, короткие транзакции, индексы, bounded pools | AT-13: ≥100 успешных бизнес-write transactions/с, включая event/audit/task, без ошибок и неограниченного роста очереди | W1,W4,W11 |
| AR-14 | Отчёты ≤30с | Материализованные run/item results, инкрементальные projections, report workers | AT-14: запрос→готовый отчёт ≤30с на согласованном объёме при фоновой нагрузке; LLM отключён и отчёт всё равно формируется | W8,W11 |
| AR-15 | Буферизация при сбоях | IndexedDB commands/drafts/audio chunks + persistent tasks/outbox + storage backpressure | AT-15: reload вкладки после offline, очередь восстанавливается; quota/disk-full явно показываются, ложного ACK нет | W4,W9,W11 |
| AR-16 | Проверка производительности | k6/browser timing, business DB benchmark, RTP fixture, fault tests, restore tests | AT-16: воспроизводимые scripts, environment manifests и сырые метрики для AT-09…15 | W11 |
| AR-17 | Сложность кейса | ScenarioVersion.difficulty, filters, assignment snapshot, progress by difficulty | AT-17: easy/medium/hard назначаются и фильтруются; изменение draft не меняет активный snapshot | W2,W3 |
| AR-18 | Аудит всех действий | Append-only security/business audit, reads/exports/login/denied, linked commands | AT-18: для каждого endpoint/permission есть audit coverage; mutation rollback не оставляет ложного success | W1,W11 |
| AR-19 | Logs ≥6 месяцев | Monthly partitions, retention ≥6 календарных месяцев, independent archive, restricted DB role | AT-19: возрастная граница и удаление проверены; запрещено удалить раздел с более свежими событиями | W1,W10 |
| AR-20 | Windows 10/11, Ubuntu 20.04+ | Browser client, supported LAN install profile | AT-20: ОС-матрица на реальных образах с keyboard/media/storage tests; версия браузера фиксируется | W9,W11 |
| AR-21 | Chrome, Firefox, Яндекс.Браузер | Standard Web APIs, capability checks, explicit Yandex run | AT-21: Chromium+Firefox e2e и отдельный smoke/e2e Яндекс; WebRTC/recording/reconnect отдельно | W4,W9,W11 |
| AR-22 | Локальный SIP-сервер | Asterisk, SIP-TLS/SRTP IP phone + browser WebRTC, no PSTN | AT-22: локальный вызов браузер/телефон, запись и stop; внешний trunk недоступен | W9 |
| AR-23 | Вертикальное масштабирование | Configurable pools/limits, external inference, no hard CPU/GPU assumption | AT-23: увеличение ресурсов и настроек без изменения доменной логики; benchmark before/after | W0,W10,W11 |
| AR-24 | Горизонтальное масштабирование | Stateless API, DB task claims, shared capacity limits, replay any node, HA data plane | AT-24: два API/workers, reconnect на другой узел, lease theft и отсутствие двойного применения | W1,W6,W10,W11 |
| AR-25 | Stop/finish преподавателем | Lesson barrier/epoch, per-run sealing, cancel interaction, preserve evidence, async assessment | AT-25: stop одновременно с command/turn/next-card/lease expiry; после барьера изменений нет; все runs terminal и отчёт доступен | W6,W7,W11 |
| AR-26 | Источник карточек задачи 2 | Approved CardTemplateVersion, system/student/mixed SourcePolicy, frozen pool, seed | AT-26: три режима; multi-category/service filters; provenance; empty pool; exhaustion; источник ученика не раскрывает автора | W2,W5,W11 |

## Обязательное из ТЗ и предметных источников сверх этих 26 строк

| ID | Источник | Требование / проектное решение | Проверка / пакет |
|---|---|---|---|
| TZ-01 | ТЗ PDF 7–9,15–16 | Локальный контур, только эмуляция; запрет runtime internet и реальных вызовов | Offline install/egress-deny, локальные assets/maps/models, dialplan test; W9,W10 |
| TZ-02 | ТЗ 15 | Задача 1 включает заполнение карточки, а не только чат | E2E incoming call→card→submit→report; W4 |
| TZ-03 | ТЗ 16 | Задача 2 выдаёт следующую случайную карточку до завершения занятия | E2E loop, stop race, stable seed; W5,W6 |
| TZ-04 | ТЗ 11–12,15 | Создать/редактировать/валидировать сценарий и эталон, включая AI; preview и контекстная коррекция | Draft/validation/publish lifecycle, generation retry, partial approval gate; W2 |
| TZ-05 | ТЗ 15–16 | Multi-select категорий; только профильные события по службе | Routing policy + service/territory filters; W2,W3,W5 |
| TZ-06 | ТЗ 12–13 | Назначения и группы; список только доступных модулей | Assignment ACL tests, start unauthorized case denied; W3 |
| TZ-07 | ТЗ 12,15–16 | Настраиваемый тайминг, default 30с, нормы/превышения/грамматика в отчёте | Timing policy anchors, deadline restart, report fields; W3,W4,W7,W8 |
| TZ-08 | ТЗ 12 | Настройка порогов ошибок/синтаксиса, AI + экспертная оценка | Versioned rubric, audited revision with reason; W2,W7 |
| TZ-09 | ТЗ 12–13 | Обратная связь, личная история ошибок, рекомендации и групповые AI insights | Reports/progress/feedback UI, background insight job; W7,W8 |
| TZ-10 | ТЗ 12–13,15 | Учебные материалы и справочная база, ручная загрузка, grammar check | Local file validation/extraction, RBAC, material versions; W2 |
| TZ-11 | ТЗ 9 | Интеграция с локальным IAM и мониторингом | IdP adapter contract + offline logins; Prometheus/export/webhook LAN only; W1,W10 |
| TZ-12 | ТЗ 10–11 | Admin users/roles/block/config/logs/backup/update/start/stop/health | Operations UI + allowlisted ops-agent + supervisor console; W1,W10 |
| TZ-13 | ТЗ 11–13 | Ограничения admin/instructor/operator, аудит изменения оценок | Negative permission matrix, resource ownership, field-level DTOs; W1,W7 |
| TZ-14 | ТЗ 14 | Отказ отдельного узла, автоматическое восстановление, диагностика/оповещения | HA profile with synchronous DB/fencing, kill-node drills, alert delivery; W10,W11 |
| TZ-15 | ТЗ 10,18 | Русский UX, desktop/mobile, минимальная сложность | Responsive layouts and keyboard/contrast; real browser/OS smoke; W4,W8,W11 |
| TZ-16 | ТЗ 14 | PostgreSQL ≥12 | Целевой pinned PostgreSQL 16 (как в core), миграции и restore на закреплённой версии; «≥12» не означает тестировать каждый major | W0,W10 |
| TZ-17 | ТЗ 16–17 | JSON/XML/CSV/SQL/PDF/DOCX/MP3/WAV; validation/integrity/compression | Import/export adapter inventory ниже, fixtures and size limits; W2,W8,W9,W10 |
| TZ-18 | ТЗ 18,20 | Исходники, архитектура, методы, ограничения, build/install/restore, перечень компонентов, сдача | Documentation/SBOM/offline bundle/репозиторий/демо; W10,W11. Презентация и docx/pdf — отдельные delivery outputs, не созданы этим архитектурным пакетом |
| DM-01 | Памятка 8,14; XLSX G–L и M–CZ | Итоговый тип по комбинации признаков, условный список служб | Golden routing fixtures с provenance; W2,W5 |
| DM-02 | Памятка 21–27 | ServiceReaction отдельно от derived CardStatus, норматив от направления | Workflow golden tests, 103/104 exceptions, overdue flags; W5,W7 |
| DM-03 | Памятка 25,32 | После завершения/отказа нет обычного редактирования; обращение к контролю отдельно | Reject terminal reaction mutation + recorded control contact; W5 |
| DM-04 | DDD обе схемы | Scenario → immutable snapshot → evidence → assessment; инфраструктура не домен | Import boundary checks, golden evidence contracts; W1…W8 |

## Форматы и противоречия

Консервативное покрытие раздела форматов: JSON — API/scenarios/profiles/logs; XML — schema-validated config/workstation/methodical metadata и legacy import/export, без автоматического исполнения; CSV — статистика; SQL — БД/migrations/backup; PDF — документы/сертификаты; DOCX/PDF — загрузка методических материалов и локальное извлечение; MP3/WAV — запись/экспорт вызовов. XLSX-классификатор — отдельный обязательный импорт исходного источника. XLSX export отчётов, расширенные heatmaps — optional из ТЗ §6, в mandatory baseline не подменяют CSV/PDF.

XML обрабатывается без external entities/сетевых ссылок. File uploads проходят размерные ограничения, MIME validation, проверку содержимого и карантин; архивы — лимиты распаковки/путей; ссылки не скачиваются автоматически. Binary recordings сохраняются с hashes и manifests; сжатие разрешено по типу, не нарушает целостность. Встроенная запись браузера может давать WebM/Opus: локальный converter экспортирует обязательные WAV/MP3; UI не обещает native MP3 во всех браузерах.

ТЗ одновременно называет export Excel/PDF optional (§6) и PDF/CSV среди форматов (§12). Для полного покрытия в плане включены CSV/PDF документы/сертификаты, Excel export оставлен расширением. «Совместимость с основными СУБД» при прямо заданном PostgreSQL трактуется как SQL/migrations и data ports для переноса; реальная поддержка второй СУБД требует отдельного объёма. Это предмет уточнения, а не обещание уже существующей DB portability.

Предложения `HTTPS на входе + закрытая сеть` и `внешний S3` уточнены по ТЗ: внутренние hop тоже защищены; storage находится на отдельном узле **внутри** локального контура. Дополнительные требования не используются для ослабления основного ТЗ.

## Как читать показатели приёмки

Заданные в ТЗ пределы не заменяются удобными percentile: в протоколе сохраняются max, p95, p99, ошибки и raw samples; любое превышение обязательного максимума в согласованном workload требует разбора. P95/p99 служат диагностикой, а не переопределением ≤2с/≤150мс/≤30с.

До нагрузочного прогона фиксируются: hardware, OS/browser, network, dataset/retention, число голосовых и текстовых сессий, длина контекста/реплик, профиль действий 100 пользователей, cold/warm state, длительность, критерий «отклик», размер отчёта. `202 Accepted` измеряется как подтверждение принятия, но не как готовый AI-ответ. Mandatory performance считается закрытым только фактическим протоколом на целевой конфигурации.
