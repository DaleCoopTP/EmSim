# ADR-027. Оператор 112: редактор сценариев преподавателя (срез 112-7)

Статус: accepted · 2026-09-26 · уточняет ADR-015/023/024/025/026 для `operator112_intake`; RFC-001 §4.5/§7.4/§13 (срез 11)

## Контекст

Срез 112-7 даёт преподавателю форму для создания собственного кейса 112
без правки JSON: он проходит его в предпросмотре и утверждает для
назначения группе (`slice-planning-112.md` §11). Пользователь принял
26.09.2026 четыре решения, сужающие и уточняющие исходную постановку:

1. Предпросмотр — сам преподаватель-автор проходит кейс, а не отдельный
   тестовый обучаемый.
2. Предпросмотр получает автоматическую оценку `operator112/rubric-v2`
   (ADR-026), видимую автору, но не входящую в отчёты/историю/прогресс.
3. Черновик и его эталон видит и правит только автор; утверждённая
   версия доступна всем преподавателям для назначения; чужой сценарий
   можно только скопировать в свой новый черновик.
4. Редактор поддерживает только `mode="full_case"` с
   `caller_mode="free_text"` (ИИ-заявитель, ADR-025) — авторинг
   подготовленного (`prepared`) диалога 112-2 и `card_only`-кейсов
   112-3 через UI не входит в этот срез (техдолг, файловый импорт
   остаётся единственным путём для них).

Сейчас код не поддерживает ни один из этих путей:

- `internal/content/http/handlers.go` регистрирует только `GET`-пути
  для сценариев; `POST/PUT /scenarios`, `.../approve` описаны в
  `openapi.yaml`, но 404 — не реализованы (`slice-2-plan.md`/§13
  RFC-001 относили их к срезу 11, общему для ДДС и 112).
- `content.Validate` (`internal/content/validate.go`) возвращает
  **первую** найденную ошибку как `*ValidationError` — подходит для
  отказа файлового импорта, но не даёт редактору полный список проблем
  формы.
- `lessons.mode`/`runs.mode` — CHECK на `('intro', 'training')`;
  `assignments.workstation_id`/`runs.workstation_id` — `NOT NULL`;
  `assignments`' PK — `(lesson_id, workstation_id)`. Ни один прогон
  сегодня не может существовать без рабочего места.
- `training.checkAssignableVersion` требует `version.Status ==
  "approved"` безусловно — черновик не может быть назначен ни на одно
  занятие, даже занятие самого автора.
- `POST /items/{id}/actions` и чтение item авторизуют только роль
  trainee, с проверкой, что `actor.WorkstationID` совпадает с
  `run.WorkstationID` (`internal/training/service.go`).
- `training.Service.enqueueEvaluateWaiting` ставит `assessment.evaluate`
  только для `lesson.Mode == ModeTraining`; отчётность
  (`internal/reporting/postgres/store.go`) и `assessment`'s
  `trainee_assessment_state` версионирование не знают о третьем режиме.

## Решение

### Владение и жизненный цикл версий

Модель авторства уже заложена в `scenarios`/`scenario_versions`
(`created_by`, `source_key` nullable, `status ∈ {draft, approved,
superseded}`, `scenario_versions_one_approved_idx`) — используется без
миграции таблиц сценариев.

- **Каждый `PUT /scenarios/{id}` создаёт новую версию `N+1` со статусом
  `draft`**, а прежняя (черновик или approved) переходит в
  `superseded` — то же самое правило, что уже было записано в
  openapi.yaml для будущего среза 11 («Правка — создаёт новую
  draft-версию»), без отдельного «редактирования на месте». Прежде
  чем принять это решение, рассматривался вариант «править черновик на
  месте, пока на него не ссылается ни один `run`»: он потребовал бы,
  чтобы `content` знал, использовала ли `training` конкретную версию
  хоть одним прогоном — обратное направление зависимости по сравнению
  с уже принятым (`training` читает `content`, никогда наоборот,
  `internal/training/ports.go`'s `ScenarioReader`). Монотонная нумерация
  версий на каждое сохранение — обычная и достаточная практика (как
  история версий документа); `base_digest` уже защищает от потерянных
  правок независимо от того, как считаются номера. Так evidence,
  оценка и снимок уже идущего/пройденного предпросмотра никогда не
  читают тело версии заново — они ссылаются на конкретный
  `scenario_version_id`, зафиксированный на старте run (не меняется),
  а не на «текущий черновик сценария».
- **Правка утверждённой версии** так же создаёт черновик `N+1` (как
  раньше — стабильное правило, не новое) — тем же путём, без разницы
  в обработке.
- **`POST /scenarios/{id}/approve`** одной транзакцией переводит
  прежнюю `approved`-версию (если есть) в `superseded` и черновик — в
  `approved`; уникальный частичный индекс не меняется.
- **Оптимистичная блокировка**: `PUT`/`approve` принимают `base_digest`
  — sha256 текущей версии, который клиент получил при последнем чтении.
  Несовпадение → `409 {code: stale_draft}`, без применения изменений
  (тот же паттерн, что и `expected_seq` для команд обучаемого, RFC-001
  §7.1, применённый к редактированию контента, а не к прохождению).
  Уточнение 27.09.2026 по ревью: digest покрывает только тело, а
  название хранится в `scenarios`, поэтому сохранение одного названия
  создаёт версию с тем же digest и вторая вкладка молча перезаписывала
  его. `PUT` дополнительно требует `base_version_id` (id прочитанной
  версии; не последняя → `409 stale_draft`), `approve` на устаревшую
  версию того же сценария отвечает `409 stale_draft`, а `PUT`/`approve`
  блокируют строку `scenarios` (`FOR UPDATE`), так что одновременные
  сохранения сериализуются и проигравшее получает `stale_draft`, а не
  нарушение уникальности номера версии.
- **Копирование.** `POST /scenarios {copy_from_version_id}` разрешён на
  любую свою версию и на любую опубликованную (`approved_at` задан) —
  чужую утверждённую или файловый сид; чужой никогда не утверждавшийся
  черновик — `404 not_found`, как и его прямое чтение (уточнение
  27.09.2026 по ревью: иначе копирование обходило приватность
  черновика) — если её
  `body.intake112.mode == "full_case"` и `caller_mode == "free_text"`;
  иначе `422 {code: unsupported_for_editor}`. Копия создаёт новую
  запись `scenarios` (свой `created_by`, `source_key=NULL`) и черновик
  `v1` с тем же телом. Копия не связана с оригиналом никаким
  указателем: она поверх собственного `scenario_id`, чтобы
  independent-редактирование одной не задевало другую и не создавало
  скрытую зависимость от того, что первичный файл/сценарий не удалён
  (в этом срезе удаления и нет).

### Без повторной JSON-Schema проверки в редакторе

`scenario.schema.json` — размеченное объединение по `exercise_type`,
которое для `operator112_intake` ожидает `card`/`reference` попросту
отсутствующими в документе; `content.Body`'s `Card`/`Reference` —
структуры без указателя и без `omitempty` (нужны ДДС всегда
заполненными), поэтому `json.Marshal` типизированного `Body`
принципиально не может воспроизвести «отсутствует», только «пустое
значение», что схема отклоняет. Полная переработка `Body` под указатели
— более широкое, затрагивающее ДДС изменение вне объёма 112-7.
Редактор поэтому не прогоняет `schema.Validator.ValidateFile` над
собранным телом вообще — только семантические проверки
`ValidateDetailed` (структурные и авторские). Файловый импорт
(`ImportScenarios`) не затронут: он по-прежнему проверяет схему первым
шагом, как раньше.

### Проверка становится списком, а не первой ошибкой

`content.Validate` перестраивается вокруг внутреннего собирателя
`issues []ValidationIssue{Path, Code, Severity, Params}` (`Severity ∈
{error, warning}`). Каждая существующая проверка добавляет issue вместо
немедленного `return`; функция продолжает обход и после первой находки.
Файловый импорт (`ImportScenarios`) не меняет внешнего поведения: он
берёт первый issue с `Severity=error` и оборачивает его в прежний
`*ValidationError{Field: issue.Path, Reason: issue.Code}` — существующие
тесты и `check.py` не видят разницы.

Новый эндпоинт `POST /scenarios/{id}/validate` отдаёт `{issues:
[]ValidationIssue}` целиком, без записи. `PUT` разрешён при наличии
`error`-issues (черновик может быть незакончен); `POST
.../preview-runs` и `POST .../approve` отклоняются с `422
{code: has_blocking_issues, details: {issues: [...]}}`, если остался
хотя бы один `error` (не `warning`).

Новые проверки авторинга (поверх уже существующих
`validateIntake112FreeTextDialogue`/`validateCallerRegexPattern`/
`validateIntake112ExpectedCardAgainstFacts`):

| Код | Severity | Условие |
|---|---|---|
| `unknown_reveal` | error | `dialogue.caller.opening.reveals[]` или `answer_variants[].reveals[]` ссылается на несуществующий `dialogue.facts[].id` |
| `unknown_expected_profile` | error | `reference.expected_profiles` ссылается на тип/карту/поле/опцию, отсутствующие в `intake-catalog.json` |
| `unknown_expected_service` | error | `reference.expected_services[]` не найден в `services.json` |
| `unknown_scoring_criterion` | error | `reference.scoring` ссылается на id, отсутствующий в `rubric.operator112.json` (rubric-v2) |
| `unknown_alternative_path` | error | `reference.alternatives` ключ — не путь `expected_card`, который уже существует в этом же эталоне |
| `unreachable_expected_field` | error (warning для `address.country/region/okrug`) | поле `expected_card`, для которого нет ни одного факта с `knowledge=initial`, либо `on_question` без `ask_patterns`, либо вообще без `card_path`, покрывающего это поле |
| `contradicts_fact` | error | уже существует (`validateIntake112ExpectedCardAgainstFacts`) |
| `profile_outside_expected_types` | error | `expected_profiles` содержит карту, которую `service_rules` каталога не связывает ни с одним `expected_types` |
| `profile_option_invalid` | error | значение поля профильной карты вне списка опций каталога |
| `services_diverge_from_rules` | warning | `expected_services` не совпадает с тем, что дали бы `service_rules` для `expected_types` (осознанная ручная поправка эталона — не ошибка) |
| `ambiguous_ask_pattern` | warning | одна и та же образцовая фраза (первый элемент `ask_patterns`, если он есть) совпадает с `ask_patterns` двух разных фактов |

Регэксп-проверки (`compile RE2`, запрет `\b`) уже существуют
(`validateCallerRegexPattern`) и просто становятся issues вместо
немедленного `return`.

**Тестер фраз.** `POST /scenarios/{id}/probe {text}` прогоняет фразу
через тот же детерминированный классификатор фактов, который
`internal/training/operator112/aicaller` использует, чтобы решить,
какие факты уже спрошены при сборке промпта модели (ask_patterns/
ask_exclude_patterns над транскриптом с одной репликой). Классификатор
выносится в чистую экспортируемую функцию без побочных эффектов и без
обращения к модели — тем самым он одновременно тестируем сам по себе
(unit) и переиспользуется и в `aicaller`, и в `/probe`, без дублирования
логики распознавания.

### Права доступа

Владелец — `scenarios.created_by`. На **черновике** (`scenario.status
!= 'approved'` на всех его версиях) для не-владельца: `GET
/scenarios/{id}`, `.../versions`, `.../preview`, `PUT`, `.../validate`,
`.../probe`, `.../approve`, `.../preview-runs` — `404 not_found`, не
`403` (не подтверждаем даже существование чужого черновика — тот же
принцип, что `training`'s "не наш item" уже применяет).
`GET /scenarios/{id}/versions`: автор видит все версии; остальные —
только опубликованные (`approved_at` задан: текущую утверждённую и
прежние утверждённые, ставшие `superseded`), без черновиков и
вытесненных черновиков; сценарий без опубликованных версий — `404`.
(Уточнение 27.09.2026 по ревью: исходная формулировка утверждала, что
`ScenarioVersions` уже так фильтрует, но фильтра не было.)
Обучаемый по-прежнему не получает доступ к `/scenarios*` (не меняется).

### Каталог 112 для инструктора

`GET /intake112/catalog` отдаёт то же содержимое, что `IntakeCatalog`
уже хранит в БД (`internal/content/postgres/intake_catalog.go`) — типы,
профильные карты (поля/вопросы/опции), `service_rules`, плюс
`services.json`'s справочник — instructor-only проекция без секретов
(этот каталог и так не содержит ничего, скрытого от обучаемого: он и
сегодня уходит в `intake_state`).

### Предпросмотр — занятие `mode=preview`

Третий режим требует миграции:

```sql
ALTER TABLE lessons DROP CONSTRAINT lessons_mode_check;
ALTER TABLE lessons ADD CONSTRAINT lessons_mode_check
    CHECK (mode IN ('intro', 'training', 'preview'));
ALTER TABLE runs DROP CONSTRAINT runs_mode_check;
ALTER TABLE runs ADD CONSTRAINT runs_mode_check
    CHECK (mode IN ('intro', 'training', 'preview'));

ALTER TABLE runs ALTER COLUMN workstation_id DROP NOT NULL;
ALTER TABLE assignments ALTER COLUMN workstation_id DROP NOT NULL;
ALTER TABLE assignments DROP CONSTRAINT assignments_pkey;
ALTER TABLE assignments ADD PRIMARY KEY (lesson_id, user_id);
CREATE UNIQUE INDEX assignments_workstation_idx ON assignments (lesson_id, workstation_id)
    WHERE workstation_id IS NOT NULL;
```

`workstation_id IS NULL` — валидно **только** когда `runs.mode =
'preview'`/`lessons.mode = 'preview'`; приложение это проверяет
(`training.Service`), не БД, тем же способом, каким сегодня
`checkAssignableVersion` — прикладная, а не CHECK-проверка.

`training.Service.StartPreview(ctx, actor auth.Principal,
scenarioVersionID uuid.UUID) (Item, error)` одной транзакцией:

1. Проверяет, что версия принадлежит автору (`created_by ==
   actor.UserID`) и `body.Intake112 != nil` (только 112 имеет смысл
   предпросматривать этим путём — ДДС не имеет редактора в этом срезе).
2. Если у автора уже есть активный `preview`-run — штатно его
   останавливает (переиспользует существующий `Stop`-путь), опираясь
   на `runs_active_user_idx` — предпросмотр не копится, только один
   активный на автора одновременно.
3. Создаёт `lessons` (`mode=preview`, `instructor_id=actor.UserID`,
   `rubric_version` = замороженная текущая `operator112/rubric-v2`,
   стандартный `Timing` рубрики 112), `assignments` без
   `workstation_id` на `actor.UserID`, запускает run/item —
   переиспользует существующий код `Start`/`offerQueueVersion`,
   ветвящийся на `workstation_id IS NULL`.

**Уточнение по факту реализации (c4):** `StartPreview` не проходит через
`ReplaceAssignments`/`Start`/`checkAssignableVersion` вообще — она строит
`lessons`/`assignments`/`runs`/`items` в одной транзакции напрямую через
store-методы и делает свои собственные проверки (`created_by ==
actor.UserID`, `status ∈ {approved, draft}`,
`ExerciseType == operator112_intake && Intake112 != nil`). Так
`checkAssignableVersion` не нужно расширять исключением: она просто не
вызывается на пути предпросмотра, а обычный путь назначения
(`ReplaceAssignments`/`Start`, используемый только для реальных занятий)
по-прежнему требует `approved` без исключений. Это не ослабляет её —
черновик по-прежнему нельзя назначить настоящему занятию с рабочим
местом, только запустить как предпросмотр его же автором.

Авторизация команд (`POST /items/{id}/actions`) реализована новой
группой ролей `auth.GroupItemActions` (`{instructor, trainee}`) на
уровне маршрута вместо прежней `GroupTrainee`-only; собственно проверку
`run.UserID == actor.UserID` и (для не-preview run) совпадения рабочего
места по-прежнему делает `training.Service.Execute` —
`workstationMatches` (уже добавленная для 4 симметричных проверок в
`service.go`) возвращает `true` без сверки РМ, когда `run.Mode ==
preview`. Чтение item (`GET /items/{id}`) новой группы не потребовало:
`auth.GroupItemRead` уже разрешает обе роли, а `ItemForInstructor`
проверяет `lesson.InstructorID == actor.UserID`, что для собственного
preview-занятия автора уже верно без изменений. Ветка `trainee` не
меняется ни в одном условии.

Завершение — существующий `POST /lessons/{id}/stop`, без изменений.

### Оценка и исключение из статистики

- `enqueueEvaluateWaiting`: условие `lesson.Mode != ModeTraining` →
  `lesson.Mode != ModeTraining && lesson.Mode != ModePreview`, т.е.
  ставится для обоих. rubric-v1-исключение (ADR-026 c4) не меняется.
- `trainee_assessment_state`-версионирование (`internal/assessment`'s
  `CreateExpertRevision`, единственное место, которое сегодня зовёт
  `BumpTraineeStateVersion` — сам auto rev=1 его не трогает) получает
  условие: пропускается, если `item.Mode == training.ModePreview`; item
  уже загружен этим методом безусловно, отдельного чтения `lesson` не
  требуется.
- **Уточнение по факту реализации (c5):** `lesson_report_rows`
  (`design-docs/contracts/schema.sql`) уже делает `JOIN workstations w
  ON w.id = r.workstation_id` — обычный, не `LEFT JOIN`. Поскольку
  preview-run хранит `workstation_id = NULL` (миграция 00017,
  `NULLIF` при `InsertRun`), это условие никогда не совпадает для
  preview, и вся строка молча выпадает из представления — без единого
  изменения SQL. `LessonReport`/CSV/PDF (`internal/reporting`),
  `ResultsFor`/`ProgressFor` (`/my/results`, `/my/progress`) читают
  только этот view, так что preview исключён из них автоматически.
  `/users/{id}/progress` и `/groups/progress` из RFC-001 §5 в текущем
  коде вообще не реализованы (см. `internal/reporting/http/handlers.go`
  — есть только `/my/results`/`/my/progress`), фильтровать пока нечего.
  Единственное реальное изменение — `GET /lessons` (список занятий
  преподавателя, `internal/training/postgres/store.go`'s
  `ListLessonsByInstructor`): в отличие от отчётности, эта таблица не
  джойнится с `workstations` и раньше показывала бы одноразовые
  `preview`-занятия вперемешку с настоящими; добавлен безусловный
  `AND mode != 'preview'` (не завися от вызывающего `state`-фильтра).
  `internal/assessment`'s `trainee_assessment_state`-версионирование
  задокументировано отдельно ниже — оно не читает этот view вообще.
- Audit и evidence для preview пишутся как для обычного training —
  предпросмотр не менее прослеживаем, просто не влияет на итог.

### ИИ-заявитель без изменений

`CALLER_REPLIER` (ADR-025) применяется как есть; предпросмотр — это
обычный `run` с точки зрения `caller.reply`. UI показывает
`CallerTurn.Source`, уже существующее поле.

## Варианты

- **Отдельная роль/эндпоинт «тестовый прогон» вместо занятия** —
  отвергнут: дублировал бы весь механизм run/item/evidence/оценки
  параллельной системой ради одного отличия (нет рабочего места) —
  занятие `mode=preview` даёт то же самое переиспользованием, а не
  копированием кода.
- **Предпросмотр без сохранения в БД (чистый клиентский симулятор)** —
  отвергнут пользователем на этапе планирования: не даёт настоящей
  оценки rubric-v2 (она требует evidence и `assessment.evaluate`,
  RFC-001 §7.4) и не проверяет реальный путь ИИ-заявителя
  (`caller.reply` вне транзакции команды, ADR-024).
- **Разрешить редактирование чужого утверждённого сценария новой
  версией того же `scenario_id`** — отвергнуто пользователем
  («новую версию создаёт только автор»): нарушило бы предположение,
  что у сценария всегда один автор на всём протяжении его версий, и
  создавало бы неочевидный вопрос «чей это теперь сценарий».
- **`content.Validate` продолжает возвращать первую ошибку, редактор
  вызывает его в цикле, снимая поля по одной** — отвергнут: quadратичное
  число запросов на форму с десятками фактов, отдельная логика на
  клиенте, которая должна знать, какое поле исправить следующим;
  собиратель issues проще и не дублирует правила на клиенте.
- **Поддержать в редакторе и `prepared`-диалог сразу** — отклонено
  пользователем 26.09 ради меньшего первого среза; полная форма
  вопрос/ответ/ветвление (`available_after`, множественные ответы)
  сложнее ИИ-профиля (плоский список фактов) и не нужна для
  демонстрации ИИ-пути, который сейчас в фокусе (112-5b/112-6).

## Последствия

- `internal/content`: `Validate` меняет внутреннюю структуру (не
  публичную сигнатуру `Validate(body, catalog) error` — новый
  `ValidateDetailed(body, catalog) []ValidationIssue` рядом, `Validate`
  становится тонкой обёрткой для импорта), новые
  create/copy/save/approve use cases, права, `IntakeCatalog` для
  инструктора, чистый классификатор фактов (используется и `probe`, и
  `aicaller`).
- `internal/training`: миграция `preview`-режима, `StartPreview`,
  расширение `checkAssignableVersion` и авторизации команд.
- `internal/assessment`: условие `trainee_assessment_state` получает
  третий режим.
- `internal/reporting`: фильтр `lesson_mode` расширяется на `preview`
  во всех местах, где уже исключается `intro`.
- `web/`: редактор (5 вкладок), обновлённый каталог/деталь сценария,
  переиспользование `Operator112ProfileCase`/`CallerChat` в
  предпросмотре, панель автооценки вне `ItemReview.tsx`.
- Коммит-план реализации — `slice-112-7-plan.md`.
