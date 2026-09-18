# Основа полного плана имплементации

Рабочие пакеты ниже включают mandatory baseline, зависимости и критерий завершения. Это dependency-based декомпозиция; календарные сроки и численность команды не выдуманы. После spike W0 каждый пакет разбивается на задачи по таблицам, handlers, UI и тестам из контрактов.

## 1. Рабочие пакеты

| Пакет | Состав реализации | Зависит от | Результат / критерий завершения |
|---|---|---|---|
| **W0. Проверка ограничений** | Зафиксировать версии/core hash; classifier parsing spike; domain workflow review; CPU/GPU caller+judge+STT/TTS benchmark; SIP browser/phone spike; определить измерения SLA, объём данных, IAM/HA adapters | Исходные документы | Доказана техническая траектория на заданном железе; воспроизводимый voice/inference benchmark; согласована граница audio/network loss; golden cases и architecture decisions готовы к реализации |
| **W1. Основа и execution** | Go module/composition, migrations, IAM sessions/permissions/resource ACL, TLS/CA, append-only audit; перенос kernel; scope/tasks/cancel/fencing; outbox/inbox; metrics/admin endpoints; common UI auth/layout; contract schemas | W0 | Atomic command + receipt + task; lease theft/cancel/duplicate/rollback tests; role-negative tests; внутренний TLS работает |
| **W2. Контент и каталог** | Classifier XLSX→staging→normalization→approval/version; services/profiles; scenarios/rubrics/workflow; materials upload/extraction; local generate/regenerate/grammar; preview/approval; system/student card templates | W0,W1 | Опубликованные immutable scenarios/rubrics/classifier, UI преподавателя; invalid references не публикуются; импорт содержит source provenance |
| **W3. Управление занятиями** | Groups, assignments, difficulty/categories/служба, lesson plans, frozen source pools, timing policy, start/admission; список назначений оператора; live monitor skeleton | W1,W2 | Instructor начинает занятие, operator запускает только своё назначение; lesson plan immutable после start; 30с default, множественные категории |
| **W4. Задача 1 и восстановление** | Interactive run/item/turn; CallerSimulator port, disclosure; per-command durable events; card UI/черновики/поля; IndexedDB queue, receipts/replay, SSE; submit/evidence; text mode | W1,W2,W3 | Полный text incoming call→dialogue+card→submit; restart не теряет ACK; delayed worker не портит run; AT-09 API/UI baseline |
| **W5. Задача 2** | CardSource policies system/student/mixed, deterministic selection, routing, ServiceReaction workflow, timestamps, derived flags, comments/dispatch/control, loop next-card; candidate from task1 with approval | W2,W3,W4 shared run infra | E2E всех источников, golden workflow 103/104 и профили ДДС, exhausted pool, сохранение semantic mistakes для оценки |
| **W6. Остановка и sealing** | Lesson/run epoch barriers; cancellation classes; stop/finish UI; batch close/recovery; supplemental offline inputs; media stop port; seal immutable manifests | W3,W4,W5 | AT-25 race suite; API stop ≤2с; все runs terminal, partial evidence сохранён; stop не ждёт judge; restart during close безопасен |
| **W7. Оценка** | Normalizers двух режимов; deterministic rules; semantic/grammar judge; schema/evidence validation; calibration fixtures; score aggregator; partial/unavailable handling; expert revisions, publication, feedback | W2,W4,W5,W6 | Подробный score breakdown, ошибки с evidence refs, audited override; technical failure не равен fail; отдельные item/run results |
| **W8. Отчётность и прогресс** | Idempotent projections, operator/group skills, report jobs CSV/PDF/сертификат; run report со временем/нормами/грамматикой; AI insights async; instructor monitor/review UI; mobile layouts | W3,W7 | История/прогресс корректны после reassessment; готовые отчёты ≤30с на согласованном наборе; no inference dependency in report request |
| **W9. Голос и поддержка клиентов** | Asterisk/локальный SIP, secure WebRTC/phone path, media gateway, STT/TTS, recording/chunks/converter, playback/barge-in epochs, media resume, admission; capability checks/browser OS matrix | W0,W1,W4,W6 ports | 20 реальных calls, transport ≤150мс, stop/reconnect/recording tests, Windows/Ubuntu Chrome/Firefox/Яндекс; external dialing impossible |
| **W10. Эксплуатация** | Production/HA deploy, local IdP/monitoring adapters, allowlist ops-agent, config UI, backup/restore/WAL+blobs, logs retention, alerts, offline update bundles, capacity configs, SBOM/runbooks | W1; W9 для media runbook | Daily backup+restore drill, kill-node/fencing, 6-month retention boundary, offline deployment; admin required capabilities с ограниченными правами |
| **W11. Общая приёмка** | API/browser/load/media benchmark; 100 users/20 sessions/100 writes; offline, security, chaos, restore; golden quality comparison; documentation and delivery package | W2…W10 | AT-01…26 и TZ/DM scenarios с протоколами; заявленные пределы подтверждены; нет обязательных функций за feature flag |

W9-spike проводится в W0, а не откладывается до готовности всего UI. Реализация W9 может идти после стабилизации W4/W6 ports, одновременно с W7/W8. W10 начинается с TLS/backup contracts в W1, а не добавляется в самом конце. Это описывает зависимости работы, не означает запуск дополнительных агентов в текущей задаче.

## 2. Вертикальные срезы и релизные ворота

1. **Foundation gate:** authenticated operator создаёт synthetic run, команда атомарно сохраняется, worker падает/восстанавливается, audit и receipt проверены. Legacy core tests + new generic execution tests проходят.
2. **Text training gate:** преподаватель публикует кейс, назначает ученику, ученик заполняет карточку с живым локальным симулятором, остановка работает, initial evidence доступен. Это внутренний срез, не соответствие всему ТЗ.
3. **Both tasks gate:** задача 2, все источники, служебный workflow, цикл карточек, оба evidence normalizer, экспертная оценка и прогресс.
4. **Voice/operations gate:** полный локальный voice path, browser matrix, offline deployment, backup/restore/HA, monitoring/security/admin UX.
5. **Acceptance gate:** одна согласованная конфигурация проходит functional, quality, performance и fault protocols. Только здесь можно объявлять mandatory baseline готовым.

Каждая функция завершена только вместе с авторизацией, audit, схемой/миграцией, восстановлением, наблюдаемостью и соответствующей UI ошибкой. Не превращать эти свойства в необязательную «полировку» после функционального демо.

## 3. Стратегия миграций и обратимости

- Исходный `orchestration-core-main` сохраняется как reference; продукт использует новые schemas. Не менять `00001_orchestration_core.sql` задним числом.
- Миграции — expand → backfill/verify → deploy consumers → contract. Пакет новой версии содержит минимально совместимую schema version; readiness не запускает приложение с неподходящей схемой.
- Новые event/schema versions сосуществуют с readers старой версии на время rolling update; upcaster явно протестирован. Изменять смысл существующего event name запрещено.
- API/worker backward compatibility проверяется смесью соседних версий. При несовместимых изменениях — controlled drain, freeze new starts, backup, migration и resume.
- Rollback приложения возможен только при совместимой schema; необратимый drop требует отдельного проверенного backup/restore плана и успешного dry-run. «Down migration» не считается восстановлением утраченных данных.
- Published scenarios/classifier/rubric не мигрируются семантически задним числом. Новый контент получает новую версию; активные runs используют старый snapshot.
- Legacy core benchmark results не получают фиктивного operator_id; они доступны отдельно или остаются read-only archive.

## 4. Проверки по слоям

### Domain / unit

Таблицы переходов reaction/run/lesson, refusal comment rules и 103/104 исключения; epochs и terminal guards; seed/source selection; difficulty/filter combination; score formula + unavailable denominator; clocks/deadlines/overrun; normalization двух режимов; redaction/provenance. Golden domain fixtures утверждаются преподавателем на примерах памятки.

### Contract

JSON Schema/OpenAPI, known/unknown enums, typed command payloads, extra/oversized fields, evidence digest, stable message identity, source/rubric version compatibility. LLM invalid JSON, missing criterion, bogus evidence ID, out-of-range score, prompt injection in student text, timeout, refusal/empty output. Local IdP/SIP/STT/TTS adapters имеют реальные recorded fixtures и интеграционную проверку.

### PostgreSQL integration

Concurrent create same key; same key/different payload; lost ACK; optimistic conflict; lesson-stop barrier; stop-run; next-card race; concurrent claim/lease theft/expired commit; duplicate judge/finalize; outbox crash/inbox replay; expired deadline after restart; DB clock boundary; partition retention; no-update permissions; cross-run item FK. Случаи crash проверяются на границах **до/после commit**, а не только штатным graceful shutdown.

### E2E и UX

Пути всех трёх ролей, обе задачи, start/stop в любой момент, история и экспертная корректировка, source pools, русский UI, responsive layout, keyboard-only, связь/несохранённые данные, reauth после offline, audio permission/устройство отсутствует, Browser storage quota. Яндекс.Браузер проверяется отдельно: Chromium engine не заменяет испытание конечного продукта.

### Качество AI

Набор эталонных кейсов стратифицирован по категории/сложности/службе/типу ошибки, включает корректные отказы и технические обрывы. Два преподавателя независимо размечают subset; разногласия обсуждаются, версии рубрик фиксируются. Измеряются precision/recall по критическим ошибкам, agreement/weighted error по критериям и баллам, ложные обвинения/галлюцинации, стабильность repeated runs, latency/tokens. Пороговые значения утверждаются до выбора модели; в исходных материалах их нет. Автопубликация оценок допускается только для калиброванного профиля, остальное `needs_review`.

## 5. Нагрузочная модель и измерения

Стартовый workload для разработки: 100 авторизованных пользователей, 20 активных исполнителей, остальные выполняют scoped reads/monitor/report; отдельно худший допустимый профиль 20 voice calls, 100 write transactions/s и конкурентная генерация/оценка. Это **проектная модель**, её параметры утверждаются в W0. Нагрузки проверяются совместно, а не только в независимых удобных микротестах. Продолжительность steady-state предлагается 60 минут + reconnect/fault episodes; soak 8 часов для утечек отдельно.

| Измерение | Начало/конец | Предел | Инструмент/доказательство |
|---|---|---|---|
| User command | Нажатие/submit → отрисованное подтверждённое состояние | ≤2с при 100 users | Browser performance marks + request/command ID, k6/API correlation |
| Cached/read view | Навигация → видимые usable data | ≤2с | Real browser measurements, cold/warm assets separately |
| LLM response | Конец принятой реплики → text/audio first usable response | Отдельный согласованный budget; candidate ≤2с | Queue wait+STT+inference+TTS breakdown, не только HTTP ACK |
| Active sessions | Изолированные одновременные call/card workflows | ≥20 | Session counters + actual interactions + transcript identity |
| VoIP transport | Capture/packetization → playout на другом конце | ≤150мс one-way LAN | Synchronized probes/loopback; jitter/loss и codec profile |
| Write throughput | Commit бизнес-транзакции с audit/task/evidence | ≥100/с | Integration load + PostgreSQL metrics; pgbench только дополнительный baseline |
| Report | Запрос → артефакт полностью доступен | ≤30с | Fixed dataset/dimensions/formats + cold cache and ongoing writes |
| Network recovery | Link restored → state/commands/media recovered | ≤30с project target при outages ≤30с | Proxy/net fault injection, snapshots/receipts/hashes reconciliation |
| Backup | Success timestamps, restore verification | Интервал ≤24ч | Manifests/checksums + restore report |

Размер report dataset должен быть зафиксирован до приёмки: users, runs/year, items/run, messages/item, period и output rows. До этого ≤30с не является безусловным обещанием для неограниченного экспорта. Большие выгрузки могут быть paginated/асинхронными, но обязательный согласованный отчёт всё равно должен завершаться в 30с, включая очередь.

Простейшая capacity оценка: arrival rate turns = active sessions / mean inter-turn interval; inference service time измеряется. Требуемая параллельность ≳ arrival rate × service time / target utilization. При burst все 20 могут говорить одновременно — отдельно проверяется burst latency. Увеличение worker pool не увеличивает модельную производительность; report/judge не должны вытеснять interactive capacity. Пределы admission видны преподавателю до start, а не после потери ответов.

## 6. Runbooks и сдача

Обязательные документы реализации: build/dev setup, offline installation, TLS/CA и hostname setup, local IAM configuration, SIP/WebRTC devices, classifier/material import, сценарии и rubric authoring, backup/restore/PITR, failure diagnostics, scaling, model manifests/quality protocol, safe software update/rollback, role administration/audit, API/schema docs, architecture/limitations, SBOM/licenses. Все manifests/version pins входят в offline bundle.

Сдача по ТЗ: репозиторий исходников, работающий прототип, презентация pptx/pdf, сопроводительный docx/pdf. Текущий Markdown/HTML пакет служит источником архитектурной части этих документов; готовую презентацию/продукт/сертификацию он не заменяет.

## 7. Неизвестные параметры, которые нельзя скрыть

| Вопрос | Принято сейчас | Когда закрыть / что изменится |
|---|---|---|
| Что входит в UI ≤2с: только control/read или полный ответ заявителя? | Control/read обязательно; full AI latency измеряем отдельно, проверяем candidate 2с | W0; модель/железо/admission |
| «Без потери данных» включает непрерывный звук IP-телефона при полном обрыве? | ACK state сохраняется; browser chunks; для phone нужен recording gateway/пауза | W0 до voice implementation; hardware/media protocol |
| Какое железо фактически будет для 100 пользователей/20 голосовых сессий? | Минимум ТЗ не повышен; GPU необязателен | W0 benchmark; CPU profile/model choice/resource approval |
| Полный список допустимых reaction transitions и критических ошибок? | Версия policy по памятке; не навязываем все промежуточные стадии | W0/W2 review with instructor; golden tests/rubric |
| Локальный IdP, SIP phones, мониторинг и cluster products? | Порты определены, vendor не зашит | W0 integration inventory; adapter package |
| Срок хранения результатов/аудио и объём отчёта? | Security logs ≥6 месяцев; остальное без автоматического удаления до policy | W0/W10; storage sizing/report load/retention |
| Требуется ли реально поддержка второй СУБД? | PostgreSQL 16; переносимость ports/formats | До утверждения implementation scope; major extra adapter work |
| Допустимые значения model quality thresholds? | Не выдуманы; critical-error dataset + expert review | До W7 acceptance; release gate |

Эти пункты не оставляют архитектурные компоненты неопределёнными: для них заданы порты, безопасные defaults и проверки. Они влияют на конкретные параметры реализации и доказательство соответствия.
