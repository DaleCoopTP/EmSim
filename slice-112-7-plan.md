# Срез 112-7 — редактор и проверка сценариев преподавателем

Дата: 2026-09-26. Статус: реализован (коммиты c1–c10 ниже).

Основание: `slice-planning-112.md` §11, решения пользователя от 26.09.2026
(ниже). Полное архитектурное обоснование — [ADR-027](design-docs/adr/027-operator112-scenario-editor.md).
Этот файл — рабочий план: что именно меняется и в каком порядке.

## 1. Пользовательский результат

Преподаватель создаёт (или копирует существующий) сценарий 112 с
ИИ-заявителем через формы, без правки JSON. Видит список ошибок проверки,
сам проходит кейс в предпросмотре (одноразовое занятие, где участник — сам
автор) с автоматической оценкой `operator112/rubric-v2`, утверждает версию
и назначает её в обычном конструкторе занятия. Обучаемый проходит
утверждённый сценарий и получает обычную оценку.

## 2. Решения пользователя (26.09.2026)

1. **Предпросмотр.** Преподаватель сам проходит кейс — не отдельный
   тестовый обучаемый. Одноразовое занятие режима `preview`: участник —
   сам автор, без рабочего места.
2. **Оценка предпросмотра.** Считается той же `operator112/rubric-v2` и
   показывается автору. В отчёты, историю, прогресс и
   `trainee_assessment_state` не попадает.
3. **Доступ.** Черновик, его эталон и предпросмотр видит и правит только
   автор. Утверждённая версия — в общем каталоге, назначать её может любой
   преподаватель. Новую версию (в т.ч. правку утверждённого сценария)
   создаёт только автор. Чужой сценарий (включая сиды) можно
   **скопировать** в собственный новый черновик.
4. **Объём редактора.** Только `mode="full_case"` +
   `caller_mode="free_text"` (ИИ-заявитель). Авторинг подготовленного
   (`prepared`) диалога и `card_only`-кейсов через UI не реализуется в
   этом срезе — уходит в техдолг (`slice-planning-112.md` §14). Такие
   сценарии по-прежнему поставляются файловым импортом.

## 3. Ключевые проектные решения

Полное обоснование — ADR-027. Сводка:

### Версии и черновики

- В сценарии не больше одного черновика одновременно. `PUT` меняет
  черновик на месте, пока на него не ссылается ни один `run` (обычный или
  preview). Если ссылка уже есть, `PUT` создаёт версию N+1 (`draft`), а
  прежняя версия получает `superseded` — существующий CHECK
  `scenario_versions_approval_shape` это допускает (обе даты утверждения
  пусты). Так снимок предпросмотра/занятия не меняется задним числом.
- Правка утверждённой версии всегда создаёт новый черновик N+1.
- `approve` одной транзакцией переводит прежнюю `approved`-версию в
  `superseded` и новую в `approved` (как обычный, так и файловый путь —
  существующий `scenario_versions_one_approved_idx` не меняется).
- Оптимистичная блокировка: `PUT`/`approve` получают `base_digest`
  редактируемой версии; несовпадение → `409 stale_draft`.

### Проверка (валидация авторинга)

- `content.Validate` перестаёт останавливаться на первой ошибке.
  Внутренний сборщик собирает `[]ValidationIssue{Path, Code, Severity,
  Params}` (`Severity` = `error`/`warning`). Наружу для файлового импорта
  поведение не меняется: `ImportScenarios` берёт первый `error` и
  оборачивает его в прежний `*ValidationError` (сиды и `check.py`
  ничего не замечают).
- Новый эндпоинт `POST /scenarios/{id}/validate` отдаёт полный список
  `issues[]`. Сохранение черновика (`PUT`) разрешено с ошибками
  (незаконченная работа); предпросмотр и `approve` требуют отсутствия
  `error` (warning не блокирует).
- Новые проверки авторинга поверх уже существующих
  (`validateIntake112FreeTextDialogue`, `validateCallerRegexPattern`,
  `validateIntake112ExpectedCardAgainstFacts`):
  - ссылки: `dialogue.caller.opening.reveals[]` → существующие
    `dialogue.facts[].id`; `facts[].card_path` → допустимый путь (уже
    есть `validIntake112CardPath`); `reference.expected_profiles` ключи
    → `intake-catalog.json`'s `types`/`profiles`, значения полей → список
    опций поля; `reference.expected_services` → `services.json`;
    `reference.scoring` id → `rubric.operator112.json` (rubric-v2);
    `reference.alternatives` ключи → существующие пути `expected_card`;
  - **недостижимый обязательный факт**: поле `expected_card`, взятое из
    факта с `knowledge="unknown"`, из `on_question`-факта без
    `ask_patterns`, или вообще не покрытое ни одним `card_path` — `error`.
    Исключение: `address.country/region/okrug` — `warning` (по инструкции
    оператор определяет их сам, не спрашивая заявителя);
  - **противоречивый эталон**: `contradicts_fact` (уже есть, code
    `contradicts_fact`); `expected_profiles`, ссылающийся на карту, не
    входящую в `expected_types` (через `service_rules`/`types`
    каталога) — `error`; значение поля карты вне списка опций — `error`;
    `expected_services`, расходящийся с тем, что дал бы `service_rules`
    для `expected_types`, — `warning` (эталон может осознанно требовать
    ручной корректировки);
  - паттерны: RE2-компиляция и запрет `\b` (уже есть,
    `validateCallerRegexPattern`); одна и та же примерная фраза
    совпадает с `ask_patterns` двух разных фактов — `warning`
    (`ambiguous_ask_pattern`).
- Тестер фраз: `POST /scenarios/{id}/probe {text}` прогоняет
  детерминированный классификатор фактов (та же функция, что
  `internal/training/operator112/aicaller` использует для сборки
  промпта — выносится в чистую, тестируемую форму без обращения к
  модели) и возвращает, какие факты сообщение открывает/спрашивает.

### Права доступа

- Владелец — `scenarios.created_by`. Для не-владельца: `GET
  /scenarios/{id}`, `.../versions`, `.../preview`, `PUT`, `.../validate`,
  `.../probe`, `.../approve`, `.../preview-runs` на **черновик** — 404
  (не 403 — не раскрываем существование чужого черновика). Утверждённая
  версия видна всем преподавателям как сейчас (список версий чужого
  сценария не показывает его черновики).
- `POST /scenarios {copy_from_version_id}` разрешён на любую версию
  (свою или чужую, включая сиды), но только если её
  `mode=="full_case"` и `caller_mode=="free_text"`; иначе `422
  unsupported_for_editor`. Копия создаёт новый `scenarios` со своим
  `created_by`, `source_key=NULL`, черновик v1 с телом копии (эталон и
  факты копируются как есть — это самостоятельная новая версия, не
  связанная с оригиналом).
- Обучаемый по-прежнему не имеет доступа к `/scenarios*`.

### Предпросмотр = занятие `mode="preview"`

- Миграция добавляет `'preview'` в CHECK `lessons.mode` и `runs.mode`;
  `assignments.workstation_id`/`runs.workstation_id` становятся
  nullable (NULL — только для `preview`, проверяется в сервисе, не в
  БД); `assignments` PK меняется на `(lesson_id, user_id)` с частичным
  `UNIQUE (lesson_id, workstation_id) WHERE workstation_id IS NOT NULL`
  вместо прежнего `(lesson_id, workstation_id)`.
- `training.Service.StartPreview(ctx, actor, scenarioVersionID)` одной
  транзакцией: создаёт `lessons` (`mode=preview`, `rubric_version` —
  текущая замороженная `operator112/rubric-v2`, `instructor_id=actor`),
  `assignments` без РМ на самого автора, запускает run/item — переиспользует
  существующий код `Start`/`offerQueueVersion`, разветвлённый по mode.
  Перед созданием — штатный `Stop` любого уже активного preview-run того
  же автора (использует существующий уникальный индекс
  `runs_active_user_idx`, конфликтов с обычными running-занятиями автора
  как участника нет — преподаватель не бывает trainee в обычных
  занятиях).
- `checkAssignableVersion` пропускает `draft`-версию только когда
  `lessonMode == preview` и `version.CreatedBy == actor.UserID`.
- Команды (`POST /items/{id}/actions`) и чтение item получают новую
  ветку авторизации: разрешено instructor, если `run.Mode == preview` и
  `run.UserID == actor.UserID`; проверка совпадения РМ
  (`actor.WorkstationID`) для такого run пропускается. Ветка trainee не
  меняется.
- Завершение предпросмотра — существующий `POST
  /lessons/{id}/stop`.

### Оценка и исключение из статистики

- `enqueueEvaluateWaiting` ставит `assessment.evaluate` для `training`
  **и** `preview` (сейчас — только `training`).
- Увеличение `trainee_assessment_state.version` (в `assessment`
  package, на новой итоговой ревизии) пропускается, когда
  `lesson.Mode == preview`.
- `GET /lessons`, `lesson_report_rows` (отчёты/CSV/PDF),
  `/users/{id}/progress`, `/groups/progress`, обучаемый `/my/results` —
  везде фильтр `mode != 'preview'` (для истории это не нужно отдельно:
  `/my/*` и так никогда не покажет чужой run, а сам автор просматривает
  свою оценку через `GET /items/{id}/assessment`, не через `/my/results`).
- Audit и evidence для preview пишутся как обычно (не менее строгие
  гарантии, просто не влияет на итоговую статистику).

### ИИ-заявитель в предпросмотре

Использует уже настроенный `CALLER_REPLIER` (без изменений). Источник
хода (`stub`/`model`/`fallback`) уже виден в `CallerTurn.Source`
(112-5b) — UI предпросмотра показывает то же поле, что и разбор
преподавателя.

## 4. API (openapi.yaml)

- `GET /intake112/catalog` (роль instructor): типы происшествий,
  профильные карты (поля/вопросы/опции), справочник служб,
  `service_rules` текущей версии каталога — то же содержимое
  `seed/intake-catalog.json`+`services.json`, уже загруженное в БД.
- `POST /scenarios {title, difficulty, copy_from_version_id?}` →
  `ScenarioDetail` с новым черновиком v1 (пустой full_case/free_text
  шаблон либо копия).
- `PUT /scenarios/{id} {base_digest, title?, difficulty?, body}` →
  `{version, digest, issues[]}` — путь уже описан в openapi.yaml,
  переписывается его поведение (сейчас 404, будет реализован).
- `POST /scenarios/{id}/validate {body?}` → `{issues[]}` без записи (для
  проверки черновика на лету до сохранения, тем же телом, что уйдёт в PUT).
- `POST /scenarios/{id}/probe {text}` → `{opened: [{fact_id, kind:
  "reveal"|"ask"}]}`.
- `POST /scenarios/{id}/approve {version_id, base_digest}` — путь уже
  описан, реализуется.
- `POST /scenarios/{id}/preview-runs {version_id}` → `{lesson_id,
  item_id}`.
- `Intake112Scenario` (schema.d.ts/scenario.schema.json): добавляется
  `caller_mode` и `dialogue.caller` (сейчас есть в Go-типах и файловой
  схеме, отсутствует в openapi-описании тела, которым делится веб).
  Новая схема `ValidationIssue{path, code, severity, message}`.

## 5. UI

- **Каталог** (`ScenarioCatalogue.tsx`): кнопка «Создать сценарий 112»,
  переключатель «Мои черновики». `ScenarioDetail.tsx`: убрать
  безусловное обращение к `call.aon`/`call.script` (пусто для
  `full_case` без предзаписанного диалога — сейчас потенциальный
  краш), показать факты/персону/эталон, кнопки «Редактировать» (только
  автору черновика), «Копировать» (всем на approved), «Предпросмотр» и
  «Утвердить» (только автору черновика без блокирующих ошибок).
- **Редактор** `/instructor/scenarios/:id/edit`, вкладки:
  1. Общее — название, сложность, АОН/местное время/часовой пояс.
  2. Заявитель — персона, вступительная реплика, `reveals`.
  3. Факты — таблица (`id/label/card_path/knowledge/value/statement/
     ask_patterns/ask_exclude_patterns/disclosure_patterns/
     answer_variants`).
  4. Эталон — выбор типов происшествия → соответствующие карты из
     каталога с их полями; `expected_card` (адрес и остальные поля) с
     кнопкой «заполнить из фактов»; службы (с пометкой «предложит
     правило» для тех, что и так войдут по `service_rules`);
     `alternatives`; `scoring` (веса/нормативы rubric-v2, из
     `rubric.operator112.json`'s `params`); `case_description`.
  5. Проверка — список `issues[]` со ссылкой на поле/вкладку и полем
     «проверить фразу» (вызывает `/probe`).

  Черновик держится в состоянии страницы; уход с несохранённым
  изменением подтверждается; конфликт `409 stale_draft` показывает
  явное сообщение и предлагает перечитать текущую версию.
- **Предпросмотр** `/instructor/preview/:itemId` — переиспользует
  `Operator112ProfileCase`/`CallerChat`; их зависимости от `me.user.id`
  для localStorage и от query-ключей `/my/*` не меняются (автор проходит
  как обычный участник своего run). После «оповестить и сохранить»
  показывается панель автооценки — общий компонент, вынесенный из
  `ItemReview.tsx`'s таблиц блоков/штрафов, без `expected`-полей.
- Конструктор занятия (`LessonDetail.tsx`) не меняется — утверждённая
  версия появляется в `useScenarios({status:"approved"})` как обычно.

## 6. Коммиты

Статус: c1–c10 реализованы 26.09.2026.

1. **c1** — [ADR-027](design-docs/adr/027-operator112-scenario-editor.md) +
   контракты: `openapi.yaml` (новые пути и схемы), `scenario.schema.json`
   (без изменений формата — только описание в openapi), `schema.sql` +
   новая миграция (`preview` mode, nullable workstation, assignments PK),
   `check.py` (если появляются новые инварианты для проверки).
2. **c2** — `internal/content`: сборщик `ValidationIssue`, новые проверки
   авторинга, `probe`-классификатор как чистая функция, unit-тесты на
   каждый новый код ошибки (битая ссылка, недостижимый факт,
   противоречивый эталон, неоднозначный `ask_pattern`).
3. **c3** — `internal/content`: create/copy/save (с версионированием
   черновика)/approve, права доступа, `IntakeCatalog` для инструктора,
   HTTP-хендлеры, audit-записи.
4. **c4** — `internal/training`: миграция применена, `StartPreview`,
   ветка авторизации команд/чтения item для preview, допуск `draft` в
   `checkAssignableVersion`.
5. **c5** — оценка preview (`enqueueEvaluateWaiting`), исключение
   preview из `trainee_assessment_state`, отчётности и прогресса.
6. **c6** — интеграционные тесты PostgreSQL: полный цикл
   create→edit→validate→preview→оценка→approve→assign→trainee run;
   конкурентный `PUT` на версию с уже начатым preview.
7. **c7** — web: `api/content.ts` хуки (`useCreateScenario`,
   `useSaveScenario`, `useApproveScenario`, `useValidateScenario`,
   `useProbe`, `useIntakeCatalog`), каталог/деталь, исправление
   краша `ScenarioDetail`.
8. **c8** — web: редактор (5 вкладок).
9. **c9** — web: предпросмотр, панель автооценки, e2e
   `operator112-editor.spec.ts`.
10. **c10** — документация (`README.md`, `seed/README.md`,
    `slice-planning-112.md`, `CLAUDE.md`, `LOG.MD`).

## 7. Definition of Done

- Через UI: создание (или копирование) → список ошибок проверки →
  предпросмотр с автооценкой → утверждение → назначение в обычном
  занятии → обучаемый проходит и получает обычную оценку rubric-v2.
- Битая ссылка, недостижимый обязательный факт и противоречивый эталон
  препятствуют предпросмотру/утверждению с конкретным полем и кодом
  ошибки; `warning` не блокирует.
- Правка сценария создаёт новую версию и не меняет уже идущее занятие,
  завершённый результат и evidence уже пройденного предпросмотра.
- Чужой преподаватель не видит и не может открыть чужой черновик (404);
  обучаемый не получает доступ к `/scenarios*` и не видит эталон нигде
  в своей проекции.
- Занятия/runs режима `preview` не появляются в списке занятий обычного
  вида, отчётах, CSV/PDF, `/users/{id}/progress`, `/groups/progress`.
- Импорт сидов, файловый путь `ImportScenarios` и существующие
  e2e-сценарии ДДС и 112 (112-1…112-6) проходят без изменений.
- `make verify`, `make verify-web`, `python3
  design-docs/contracts/check.py`, `make test-integration`,
  `cd web && npm run lint && npm run test:e2e` — зелёные.

## 8. Вне объёма этого среза

- Авторинг подготовленного (`prepared`) диалога и `card_only`-кейсов
  через UI (техдолг) — такие сценарии поставляются только файловым
  импортом.
- Генерация сценария целиком ИИ.
- Редактор ДДС (срез 11 общего плана).
- Архивирование/удаление сценариев.
- LLM-критерии (`operator112/rubric-v3`) — по-прежнему после этапа 2
  среза 112-5b.
- **Уточнение по факту реализации:** вкладка «Эталон» правит
  `expected_types`/`case_description`/`expected_services`/`expected_card`
  (адрес, статус заявителя, возраст, жалоба, число пострадавших) — этого
  достаточно для прохождения `Validate`/`ValidateDetailed` без ошибок и
  для реальной оценки rubric-v2 по адресу/описанию/штрафам. `expected_profiles`
  (эталон профильных карт 104/101), `alternatives` (доп. формулировки) и
  `scoring` (поправки весов рубрики) остаются необязательными полями
  схемы без собственного UI — задаются только копированием версии, где
  они уже есть (файловый импорт), не создаются заново через редактор. Это
  не блокирует ни один пункт Definition of Done ниже (все они — про
  версии/права/предпросмотр/исключение из отчётности, не про полноту
  каждого поля эталона), но сужает то, какую именно оценку получит
  созданный через UI сценарий по блоку профильных карт (`PROFILE_CARDS`
  получит 0, как «эталон не задан», 112-6/ADR-026).
