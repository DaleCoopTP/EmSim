# План среза ДДС-6: настройка занятия

Дата: 2026-09-29. Статус: реализован (c1–c10); решения зафиксированы в [ADR-035](design-docs/adr/035-dds-lesson-settings.md).

## Контекст

`slice-planning-dds.md` §ДДС-6 требует от формы занятия три вещи:
- нормативы (сейчас жёстко 30/30/180);
- случайное заполнение очередей по категориям и уровню;
- веса критериев и порог «зачтено» на занятие.

Требования ТЗ: TZ-11 (тайминг 30 с по умолчанию, пороги), TZ-16 (категории, случайная карточка). ADR-032 прямо откладывает пересмотр весов и порога до ДДС-6.

Что есть сейчас (проверено по коду):
- **Нормативы.** Backend уже принимает `timing` в `POST /lessons` (`internal/training/service.go:180-197`), но веб-форма всегда шлёт 30/30/180 (`web/src/routes/instructor/Lessons.tsx:38`).
- **Ошибка с дедлайном.** Живой дедлайн первичного решения считается от `open_s`, а не от `primary_s`: `Deadlines{OpenAt: openAt, PrimaryAt: openAt}` (`service.go:1191`). Сейчас это незаметно, потому что оба значения равны 30. Оценка `T_PRIMARY` при этом верна: лимит берётся из evidence через `timingLimit` в `internal/assessment/dds/rules.go:116`.
- **Веса и порог.** Их можно задать только в файле рубрики, поправить можно только `reference.scoring` сценария (`assessment.Merge`, `internal/assessment/rubric.go:182`). Порог (`PassThreshold`) не переопределяется нигде. Уровня занятия для этих настроек нет.
- **Очереди.** Заполняются только вручную (`ReplaceAssignments`, `service.go:247`). Поля категории в сценарии нет. Ближайший аналог — `card.incident.type_code`.

## Решения (пользователь, 29.09)

1. **Категория** = первые две цифры `card.incident.type_code`, то есть раздел классификатора (14 — ЖКХ, 22 — медицина…). Сиды не меняются. Названия разделов появятся с реальным классификатором в ДДС-5; до этого UI показывает код и названия типов внутри раздела.
2. **Уровень → сложность** — фиксированные диапазоны: easy 1–3, medium 4–6, hard 7–10. Это константа в коде и в ADR.
3. **Случайность — при назначении.** Сервер один раз предлагает очереди со своим порядком на каждом РМ. Преподаватель видит результат, правит его и сохраняет прежним `PUT /assignments`. Выдача карточек, evidence и отчёты не меняются.
4. **Веса занятия главнее весов сценария.** Порядок: база рубрики → веса и порог занятия → из `reference.scoring` сценария только `disabled`/`critical`. Веса сценария применяются, только если в занятии своих весов нет.

Решения исполнителя (фиксируются в ADR-035):

- **Только ДДС.** Для 112 `scoring` отклоняется: в его рубрике есть штрафные критерии, отдельная постановка нужна.
- **Где хранится.** Новая колонка `lessons.scoring jsonb NULL`: `{weights: {id: w}, pass_threshold}`.
  - `NULL` — поведение прежнее. Старые занятия и запечатанные входы не меняются.
  - `weights` — полный набор критериев замороженной `rubric_version`, каждый ≥ 0, сумма = 100. Это та же конвенция, что проверяет `check.py`.
  - Вес 0 у некритического критерия делает его справочным (`score.go` уже так обрабатывает).
  - `pass_threshold` — от 0 до 100. `critical_cap` не настраивается.
- **Когда настраивается.** Нормативы и оценивание меняются только у черновика занятия, через `PATCH /lessons/{id}`. Их фиксирует старт: элементы копируют `lesson.Timing` при выдаче, а оценка читает `lesson.scoring`.
- **Границы нормативов.**
  - `open_s` 10–300;
  - `primary_s` от `open_s` до 600;
  - `complete_s` 60–3600;
  - `spawn_every_s` — как сейчас.

  `dds.IncomingRingS` (30 с) и `expects.within_s` у докладов остаются в сценарии, это вне среза.
- **Случайное заполнение.** Из выборки исключаются сценарии с событиями `spawn_card`: их очередь связана жёстким порядком, его проверяет `checkSpawnQueuePlan`. Внутри одной очереди сценарии не повторяются. Если сценариев меньше запрошенного числа, ответ `422 not_enough_scenarios` с числом доступных.

## Коммиты

**c1 — `docs: DDS-6 plan and ADR-035 (lesson settings)`.**
- `slice-dds-6-plan.md` (этот план).
- `design-docs/adr/035-dds-lesson-settings.md`: решения выше. Отвергнутые варианты: случайность при выдаче, поле `category` в сценарии, приоритет весов сценария. Последствия: ADR-013 получает третий слой, ADR-032 закрывает отложенный пункт.
- Обновить `adr/README.md`, `slice-planning-dds.md` (ДДС-6 → «в работе»), `LOG.MD`.
- Проверка: `python3 design-docs/contracts/check.py`.

**c2 — `contracts: lesson timing bounds, lesson scoring, random fill`.**
- `openapi.yaml`:
  - границы у `Timing`;
  - `LessonScoring {weights, pass_threshold}` в `Lesson` (только чтение);
  - `PATCH /lessons/{id}` (`LessonSettingsPatch {timing?, scoring?|null}`: 409, если не черновик; 422 при ошибке проверки);
  - `GET /lessons/{id}/rubric` — рубрика занятия: критерии `id/title/kind/weight/critical` из `rubric_version` с учётом весов занятия, плюс `pass_threshold`, `default_weights`, `default_pass_threshold`;
  - `GET /scenarios/categories?exercise_type&service` → `[{code, type_names[], count_by_level{easy,medium,hard}}]`;
  - `POST /lessons/{id}/assignments/draw` (`{categories[], count, rows:[{workstation_no,user_id}]}` → `Assignment[]` без сохранения; код ошибки `not_enough_scenarios`).
- `schema.sql`: `lessons.scoring jsonb` + `CHECK` на объект.
- Проверка: `check.py`; `cd web && npm run gen`.

**c3 — `training: configurable DDS timing and primary deadline fix`.**
- `service.go`:
  - вынести проверку нормативов в функцию `validateTiming` с границами ADR-035;
  - `PrimaryAt = now + PrimaryS` в `service.go:1191`; найти `grep`'ом другие места, где считается `PrimaryAt`, если они есть;
  - новый метод `UpdateLessonSettings`: `LockUpdate`, только владелец и только черновик, аудит `lesson.update`.
- `http/handlers.go`: маршрут `PATCH /lessons/{id}`.
- Тесты:
  - границы;
  - `primary_s=60` даёт дедлайн через 60 с;
  - `PATCH` у запущенного занятия → 409, у чужого → 404;
  - 112 отклоняет `timing`.
- Проверка: `go test ./internal/training/...`.

**c4 — `training: lesson scoring storage and validation`.**
- `migrations/00021_lesson_scoring.sql`: `ALTER TABLE lessons ADD COLUMN scoring jsonb`.
- `training.Lesson.Scoring *LessonScoring`; чтение и запись в `training/postgres/store.go`; поле в JSON ответа.
- Проверка весов по замороженной рубрике. В `internal/content/rubric.go` добавить `RubricCriteriaFor(version) ([]RubricCriterionRef, error)` по образцу `loadRubricCriterionIDs`: список id, сумма 100, вес ≥ 0, порог 0–100, только `dds_processing`. `null` сбрасывает к значениям по умолчанию.
- Тесты: `service_test`/`lesson_settings_test` — лишний или недостающий id, сумма ≠ 100, 112, сброс.
- Проверка: `go test ./internal/training/... ./internal/content/...`; `make test-integration` (миграция).

**c5 — `assessment: lesson scoring layer in rubric_effective`.**
- `rubric.go`: `MergeLesson(base, lesson *LessonScoring, scenario *content.Scoring)` в порядке из решения №4. Если у занятия есть свои веса, `scenario.Weights` игнорируются; `disabled`/`critical` сценария применяются всегда. Прежний `Merge` оставить обёрткой с `lesson=nil`.
- Применить `MergeLesson` во всех трёх местах сборки рубрики, чтобы они не расходились:
  - `sealInputForItem` (`service.go:153-157`): читать занятие через существующий порт `LessonReader.LessonByID`;
  - ветки `CreateExpertRevision` (`service.go:510-518`) и `Get` (`service.go:667-673`) без итоговой ревизии.
- `GET /lessons/{id}/rubric` в `internal/assessment/http` (владелец занятия, через `LessonReader`).
- `revision.go` уже берёт порог из `effective`, менять не нужно.
- Тесты:
  - порядок слоёв;
  - порог 80 при балле 75 → `passed=false`;
  - вес 0 → критерий справочный;
  - `scoring=NULL` → результат побайтно равен прежнему `Merge`;
  - экспертная ревизия без авто-оценки использует веса занятия.
- Проверка: `go test ./internal/assessment/...`.

**c6 — `content, training: scenario categories and random queue fill`.**
- `content`: `ListAssignableDDS(ctx, filter{service, categories, difficultyMin/Max})` — утверждённые неархивные версии ДДС с `type_code`/`type_name`, без `spawn_card`. Плюс `Categories(service)` для `GET /scenarios/categories`. Если JSON-запрос к `body` окажется медленным, выделить `type_code` отдельной колонкой через миграцию; но при десятках сценариев фильтрации в Go достаточно.
- `training`:
  - `LevelDifficultyRange(level)` (1–3 / 4–6 / 7–10);
  - `DrawAssignments(ctx, actor, lessonID, in)`: для каждой строки берёт службу обучаемого, выборку по категориям и уровню, перемешивает (`math/rand/v2`, источник внедряется в тестах) и берёт `count` без повторов;
  - проверки те же, что в `ReplaceAssignments` (активные РМ и обучаемые, `service_code`); для hard с `count > 1` нужен `spawn_every_s`;
  - ничего не сохраняет и не пишет в аудит.
- Порт `ScenarioReader` в `training` расширяется узким методом.
- Тесты:
  - фильтр по категории, уровню и службе;
  - сценарии со `spawn_card` исключены;
  - нехватка сценариев → `not_enough_scenarios`;
  - две строки получают независимый порядок;
  - архивные и черновые сценарии не попадают.
- Проверка: `go test ./internal/content/... ./internal/training/...`.

**c7 — `reporting: lesson settings in the report`.**
- В отчёт занятия (JSON, CSV-заголовок, PDF-шапка) добавляются нормативы, порог и признак «веса изменены преподавателем».
- Балл и `passed` уже считаются по `rubric_effective`, их не менять.
- Тесты: `csv_test.go`, `pdf_test.go`.
- Проверка: `go test ./internal/reporting/...`.

**c8 — `web: lesson settings, weights and random fill`.**
- `Lessons.tsx`: поля нормативов для ДДС (по умолчанию 30/30/180) с теми же границами.
- `LessonDetail.tsx`, для черновика:
  - блок «Нормативы» с редактированием через `PATCH`;
  - блок «Оценивание»: таблица критериев из `GET /lessons/{id}/rubric` с весом и суммой (кнопка сохранения неактивна, пока сумма ≠ 100), порог и кнопка «Сбросить к рубрике»;
  - диалог «Заполнить случайно»: категории с чекбоксами и числом сценариев уровня занятия, число карточек на РМ. Результат попадает в редактируемые очереди строк, сохранение — обычной кнопкой.
- После старта оба блока только для чтения.
- `LessonReport.tsx`: шапка с настройками.
- Проверка: `make verify-web`, `cd web && npm run lint`.

**c9 — `test: DDS lesson settings through api and worker`.**
- `test/integration/dds_lesson_settings_test.go`, реальные api и worker, `ASSESSMENT_JUDGE=off`:
  - `PATCH` нормативов и весов у черновика;
  - `draw` → `PUT` → старт;
  - дедлайн `primary` элемента = `primary_s`;
  - после закрытия `assessment_inputs.rubric_effective` содержит веса занятия, а `disabled` сценария соблюдён;
  - `passed` считается по порогу занятия;
  - `PATCH` после старта → 409.
- e2e `web/e2e/dds-lesson-settings.spec.ts`: создать занятие с `primary_s=45`, задать веса и порог, заполнить случайно, запустить.
- Проверка: `make test-integration`, `cd web && npm run test:e2e`.

**c10 — `docs: DDS-6 complete`.**
- Обновить `CLAUDE.md` (текущее состояние, ADR-035), `README.md` (форма занятия), `slice-planning-dds.md` (ДДС-6 → реализован), итог в `slice-dds-6-plan.md`, `LOG.MD`.

## Проверка

- На каждом коммите с кодом — `go test` затронутых пакетов.
- После c2 — `check.py`.
- В конце — `make verify`, `make verify-web`, `npm run lint`, `make test-integration`, `npm run test:e2e` (нужен Docker).
- Вручную (`make demo`):
  - занятие ДДС `medium` со случайным заполнением из разделов 14 и 22;
  - `primary_s=45`: таймер в карточке и мониторе показывает 45 с;
  - порог 90 и вес `D_PRIMARY` 40: разбор показывает новые веса, отчёт — порог и «не зачтено».

## Риски

- **Бедная выборка по уровням.** У сидов ДДС сложность 1–4, поэтому на hard выборка пуста, а на medium есть только `pipe-burst`. UI честно показывает нули; наполнение — задача ДДС-5.
- **Разделы классификатора.** Две цифры `type_code` как раздел — допущение до получения реального классификатора (ДДС-5). Если структура окажется иной, меняется только функция категории.
- **Сброс весов из-за исключений сценария.** Веса занятия и `disabled` сценария вместе могут дать сумму применимых весов 0; тогда `Score` уже даёт балл 0. Проверка при сохранении этот случай не ловит — фиксируется тестом и в ADR.
- **Старые занятия.** Существующие черновики и запущенные занятия получают `scoring=NULL`, их поведение не меняется. Исправление `PrimaryAt` затрагивает только новые выдачи.

## Итог реализации

Коммиты c1–c10 выполнены по плану. Отличия от текста плана:

- **CSV не менялся.** Он остаётся плоской таблицей карточек; нормативы, порог и пометка об изменённых весах есть в JSON отчёта, на экране и в шапке PDF. Строки над заголовком сломали бы импорт в электронные таблицы.
- **Схема версии.** Миграция `00021` потребовала поднять `ExpectedSchemaVersion` до 21 (`internal/platform/postgres`), иначе `Ready()` отказывал процессам.
- **Исходный `defaults` для проверки весов** — `content.DDSRubricDefaults(version)` по образцу `loadRubricCriterionIDs`, поддерживает v1–v3.
- **Проверка вручную.** Браузерный сценарий `web/e2e/dds-lesson-settings.spec.ts` прогнан на локальных api и worker поверх отдельного PostgreSQL, без Docker (демона не было); `web/e2e/run.mjs` с compose не запускался.
- Случайное заполнение возвращает `savedVersionIds` строки — в редакторе очередь показывается числом «Сохранено кейсов: N», а не названиями.
