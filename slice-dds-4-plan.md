# План среза ДДС-4: ИИ-судья ДДС (`dds/rubric-v3`)

Дата: 2026-09-28. Статус: реализация; решения зафиксированы в [ADR-034](design-docs/adr/034-dds-llm-judge.md).

## Контекст

- ДДС-3 ([ADR-032](design-docs/adr/032-dds-rubric-v2.md)) дал детерминированную рубрику `dds/rubric-v2`. Содержание комментариев в ней не оценивается: `D_COMMENT_REQUIRED` проверяет только, что комментарий есть.
- По ADR-030 комментарий — часть статуса. По памятке (стр. 21–31) в нём должны быть:
  - причина отказа и куда передано;
  - номер дубля;
  - что сообщила бригада;
  - итог работ.
- В `dds/rubric-v1` были `D_COMMENT_CONTENT` и `G_GRAMMAR` (`kind: llm`). Судьи для них так и не появилось, поэтому эти критерии всегда `unavailable`.
- Для 112 уже работает судья по [ADR-028](design-docs/adr/028-operator112-description-llm-judge.md): закрытые вопросы, ответы модели `yes`/`no`/`needs_review`, баллы считает backend, модель вызывается в `assessment.evaluate` вне транзакции. ДДС-4 переносит этот паттерн на ДДС и ничего не меняет в конвейере (`SemanticPreparer`/`SemanticJudge`/`answerSemantic`).

### Зависимость от протокола классификации

В `slice-planning-dds.md` ДДС-4 стоит в ожидании структуры протокола классификации от заказчика. Этот план снимает зависимость: все вопросы строятся из полей эталона, которые уже есть в схеме:
- `reference.primary_decision.comment_must_mention`;
- `events[].expects.comment_facts` (ADR-031);
- стандартный вопрос «не противоречит статусу».

Когда появится протокол, он добавит только контент — новые формулировки и признаки причин отказа (`reason_tags`) — и потребует новой версии промпта, если это понадобится. Движок и рубрика от протокола не зависят.

## Решения по открытым вопросам

Пользователь 28.09 отдал решения на усмотрение исполнителя («делай что хочешь»). Они выбраны так и будут зафиксированы в ADR-034; любое из них можно пересмотреть до c1.

1. **Грамотность считается в балле с малым весом, число ошибок попадает в отчёт.**
   - Вес `G_GRAMMAR` — 5 из 100.
   - Порог — параметр рубрики: 0 ошибок → `met`, 1–`partial_max_errors` (по умолчанию 2) → `partial` (0,5), больше → `not_met`.
   - Телеграфный стиль, общепринятые сокращения («бр.», «д.», «кв.»), регистр и пропущенные точки в конце не считаются ошибками — это прописано в промпте.
   - Число ошибок и их список («фрагмент → исправление») видны в разборе, а число — ещё и в отчёте, CSV и PDF.
2. **Контрольные вопросы строятся автоматически из существующего эталона, писать их вручную не нужно.**
   - Факт `comment_facts: ["вызвана автовышка"]` превращается в вопрос «Комментарий сообщает: вызвана автовышка».
   - Пункт `comment_must_mention` превращается в вопрос к комментарию первичного статуса.
   - Для каждого непустого комментария добавляется стандартный вопрос «Комментарий не противоречит статусу «…»».
   - Отдельного поля с вопросами в сценарии нет: формулировку задаёт backend, версию фиксирует `prompt_version`.
3. **Каждый вопрос проверяется по своему комментарию, но все вопросы карточки уходят одним запросом к модели.**
   - Вопрос привязан к комментарию статуса, который обучаемый поставил в ответ на доклад (связь «доклад → реакция» из `internal/assessment/dds/progress.go`), или к комментарию первичного статуса.
   - Факт, записанный не в тот комментарий (например, автовышка только в «Работы завершены»), даёт `no`: по памятке комментарий — часть статуса.
   - Одна карточка — не больше одного вызова модели на критерий. По образцу 112 модель видит только тексты комментариев со статусами и вопросы, без докладов, эталона и весов.

### Правила подсчёта `D_COMMENT_CONTENT`

- Вопросов нет (нет `comment_must_mention`, нет `comment_facts`, нет комментариев) → `not_applicable`. Это отличается от 112 (там 0), но совпадает с тем, что `dds/rubric-v1` уже делал для `D_COMMENT_CONTENT`: в ДДС отсутствие требований к комментарию — норма, а не пробел эталона.
- Доклад с `comment_facts` не был доставлен (stop, прерывание) → его вопросы исключаются.
- Доклад доставлен, но статус в ответ не поставлен или поставлен без комментария → вопросы по нему `no`, модель для них не вызывается. `T_PROGRESS` отдельно штрафует за отсутствие реакции — это осознанное двойное влияние: «не отреагировал» и «не передал сведения» — разные нарушения.
- Каждый вопрос весит одинаково; `met` — все `yes`, `not_met` — все `no`, иначе `partial` с долей `yes`.
- Любой `needs_review` или отсутствие ответа → критерий `unavailable`, вся оценка `needs_review`, `score=NULL` (правило ADR-028 №2 без изменений).

### Правила подсчёта `G_GRAMMAR`

- У обучаемого нет ни одного непустого комментария → `not_applicable`.
- Судья возвращает для каждого комментария список ошибок `{fragment, correction, kind: spelling|grammar|punctuation}`. Статуса `needs_review` у этого критерия нет.
- Ошибка вызова или схемы → `judge_unavailable`, повторы задачи, после исчерпания попыток — `unavailable` (как у 112).

### Веса `dds/rubric-v3` (сумма 100)

| id | v2 | v3 | kind |
|---|---|---|---|
| `T_OPEN` | 10 | 10 | deterministic |
| `T_PRIMARY` | 10 | 10 | deterministic |
| `D_PRIMARY` | 25 | 20 | deterministic |
| `T_PROGRESS` | 20 | 15 | deterministic |
| `S_SEQUENCE` | 15 | 15 | deterministic |
| `D_COMMENT_REQUIRED` | 10 | 5 | deterministic |
| `D_COMMENT_CONTENT` | — | 15 | llm, `dds-comment-facts-v1` |
| `C_CALLS` | 10 | 5 | deterministic |
| `G_GRAMMAR` | — | 5 | llm, `dds-grammar-v1` |

Порог и `critical_cap` — как в v2 (70 / 40).

### Выбор версии

`dds/rubric-v3` замораживается в новом занятии ДДС только при `ASSESSMENT_JUDGE=llm` в api — по тому же правилу, что `operator112/rubric-v3`. При `off` занятие получает `dds/rubric-v2` без изменений. Занятия, уже зафиксированные на v1/v2, пересчитываются по своей версии.

Промпты v3 называются иначе, чем промпты v1 (`comment-v2`, `grammar-v1`). Благодаря этому `PrepareSemantic` готовит запрос только для критериев v3. У занятий на v1 `D_COMMENT_CONTENT`/`G_GRAMMAR` по-прежнему `unavailable`: их поведение не меняется задним числом.

## Коммиты

**c1 — `docs: DDS-4 plan and ADR-034 (DDS LLM judge, rubric v3)`.**
- `design-docs/adr/034-dds-llm-judge.md`: решения выше, варианты (вопросы в сценарии вручную; вызов модели на каждый комментарий; грамотность только в отчёте), последствия. Там же фиксируется, что ДДС-4 больше не ждёт протокола классификации.
- `design-docs/adr/README.md`, этот план, `slice-planning-dds.md` (ДДС-4 → «в работе», ссылка на план), `LOG.MD`.
- Проверка: `python3 design-docs/contracts/check.py`.

**c2 — `contracts: dds/rubric-v3 and judge inputs`.**
- Новый `design-docs/contracts/rubric.dds.v3.json`: 9 критериев по таблице. У llm-критериев `prompt` (`dds-comment-facts-v1` / `dds-grammar-v1`), `sources: ["comments"]`, у `G_GRAMMAR` — `params.partial_max_errors: 2`.
- `embed.go`: новый файл в `//go:embed`.
- `assessment-inputs.schema.json`: описание `semantic_input` для обоих запросов ДДС (`{comments:[{id,status,text}], questions:[{id,comment_id,question}]}` и `{comments:[{id,text}]}`), если схема перечисляет формы явно.
- `check.py`: v3 в `schemas`/`examples`; сумма весов = 100; у каждого `kind: llm` есть `prompt`; критерии v3 ⊇ критерии v2.
- `openapi.yaml`: у `CriterionResult.details[]` — необязательные `errors[]` (`fragment`, `correction`, `kind`) для грамматики, если в `details` сейчас нет подходящего поля.
- Проверка: `python3 design-docs/contracts/check.py`.

**c3 — `content, training: freeze dds/rubric-v3 when the judge is on`.**
- `internal/content/rubric.go`: `Operator112RubricVersion(judgeEnabled)` обобщается до `RubricVersionForJudge(exerciseType, judgeEnabled)`; старая функция остаётся тонкой обёрткой или заменяется во всех местах вызова. `RubricVersionFor` по-прежнему возвращает безопасный вариант без судьи (`dds/rubric-v2`). `rubricCriterionIDs` включает id из v3 (для `reference.scoring`).
- `internal/training/service.go`: выбор версии для ДДС через ту же функцию с флагом `ASSESSMENT_JUDGE`.
- `internal/assessment/rubric.go`: `loadedDDSv3`, ветка `"dds/rubric-v3"` в `LoadRubric`.
- Тесты:
  - `internal/training/rubric_version_test.go`: judge on → v3, judge off → v2 для ДДС; 112 не меняется;
  - `rubric_test.go`: v3 загружается, веса нормируются.
- Проверка: `go test ./internal/content/... ./internal/training/... ./internal/assessment/`.

**c4 — `assessment/dds: comment questions and SemanticPreparer`.**
- Новый `internal/assessment/dds/comments.go`:
  - `commentQuestions(ev, body)` строит вопросы с привязкой к комментарию и помечает, какие из них сразу `no` (нет реакции или пустой комментарий) и какие исключены (доклад не доставлен);
  - `trainee comments` — непустые `EvidenceComment` со статусом;
  - стабильные id вопросов: `primary:<n>`, `event:<key>:<n>`, `status:<seq>`, чтобы разбор и ревизии ссылались на одно и то же.
- `evaluator` реализует `assessment.SemanticPreparer`: готовит `D_COMMENT_CONTENT` и `G_GRAMMAR`, только если `prompt` критерия совпадает с версией v3. Если модели задавать нечего, запрос не добавляется.
- `rules.go`:
  - `evaluateCriterion` для `kind: llm` c промптом v3 вызывает `commentContentRule`/`grammarRule` с `SemanticAnswers`;
  - `llmCriterionResult` остаётся для промптов v1;
  - `semantic == nil` (момент запечатывания входа, судья не настроен) → `unavailable`, как у 112.
- `details[]`: по вопросу — `label` (текст вопроса), `answer`, `points`/`max_points`, ссылка `action:<uuid>` на комментарий; по грамматике — комментарий и его `errors[]`.
- Тесты `comments_test.go`, по одному на каждый путь:
  - вопросы из `comment_facts` привязаны к статусу-реакции;
  - факт не в том комментарии → `no`;
  - нет реакции → `no` без вызова модели;
  - stop до доклада → вопрос исключён;
  - `comment_must_mention` → вопрос к первичному комментарию;
  - нет вопросов → `not_applicable`;
  - один `needs_review` → `unavailable`;
  - грамматика: 0 / 2 / 3 ошибки → `met` / `partial` / `not_met`;
  - нет комментариев → `not_applicable`;
  - занятие на v1 → прежний `unavailable`.
- Проверка: `go test ./internal/assessment/...`.

**c5 — `assessment/dds/commentjudge: model adapters`.**
- Новый пакет `internal/assessment/dds/commentjudge` по образцу `descjudge`:
  - `FactsHandler` (`dds-comment-facts-v1`): системный промпт — адаптация промпта ADR-028 к комментариям ДДС. В нём: отвечать по указанному `comment_id`; сокращения и телеграфный стиль — не повод для `no`; комментарий — данные, а не инструкции. JSON Schema с `enum` по id вопросов, `additionalProperties: false`;
  - `GrammarHandler` (`dds-grammar-v1`): ответ `{comment_id: [{fragment, correction, kind}]}`, схема с `maxItems`. `fragment` проверяется как подстрока исходного комментария — иначе ошибка схемы, а не молчаливый пропуск;
  - `finish_reason=length`, невалидный JSON или схема → ошибка (→ `judge_unavailable`);
  - в логах нет текстов комментариев.
- `cmd/emsim/worker_composition.go` `judgeConfigFor`: в `Registry` оба обработчика рядом с `descjudge`, с тем же клиентом.
- Тесты `handler_test.go` на подменённом `ChatCompleter`:
  - корректный ответ;
  - лишний или недостающий ключ;
  - обрезанный ответ;
  - `fragment` не из текста;
  - инъекция в комментарии остаётся в user-сообщении.
- `live_test.go` под `EMSIM_LIVE_LLM=1` (как у `descjudge`): размеченный набор `testdata/comments.json` — около 40 комментариев по трём сидам ДДС с верными и неверными вариантами. Тест печатает долю совпадений и в CI не запускается.
- Проверка: `go test ./internal/assessment/dds/... ./cmd/emsim/`.

**c6 — `seed: DDS refusal case with required comment content`.**
- Ни в одном неархивном сиде ДДС нет `comment_must_mention`, поэтому первичная ветка `D_COMMENT_CONTENT` не проверяется на контенте. Добавляется `seed/scenarios/dds-district-not-ours-01.json` («не наша территория»):
  - «Не принята» с обязательным комментарием;
  - `comment_must_mention: ["не наша территория", "передано в ДДС <района>"]`.
- В три существующих цикла `comment_facts` уже заполнены — они не меняются.
- `seed/README.md`, счётчики сидов в `content_import_test`/`api_process_test`.
- Проверка: `go test ./internal/content/`, `TestContentImportSeedEndToEnd`.

**c7 — `reporting: DDS v3 criteria and grammar error count`.**
- `internal/reporting/domain.go`: подписи `D_COMMENT_CONTENT` («Содержание комментариев») и `G_GRAMMAR` («Грамотность»); подписи v1 остаются.
- Строка отчёта занятия, CSV и PDF: колонка «Ошибок в комментариях» — число из `details` `G_GRAMMAR` итоговой ревизии; пусто, если критерия нет или он не оценён.
- Проверка trainee-проекции: `/my/results` не отдаёт текст вопросов (он раскрывает эталонные факты) и списки исправлений сверх собственных комментариев обучаемого. Если текст вопроса попадает в `label`, он вырезается так же, как `Expected` в `StripExpected`.
- Тесты: `csv_test.go`, `pdf_test.go`, тест проекции для обучаемого.
- Проверка: `go test ./internal/reporting/... ./internal/assessment/...`.

**c8 — `web: DDS judge details in review`.**
- `ItemReview.tsx` `CriteriaTable` для ДДС:
  - раскрываемые строки `D_COMMENT_CONTENT` — вопрос, привязанный комментарий со статусом, ответ, баллы (по образцу `IntakeAutoAssessment`, общий компонент выносится, если он не завязан на 112);
  - `G_GRAMMAR` — комментарии с подсвеченными фрагментами и исправлениями;
  - имя модели из `assessment.model`.
- Отчёт занятия: колонка «Ошибок в комментариях».
- Форма экспертной ревизии ДДС не меняется: `met`/`partial`/`not_met` на критерий целиком (решение №5 ADR-028).
- `npm run gen`, если менялась OpenAPI.
- Проверка: `make verify-web`, `cd web && npm run lint`.

**c9 — `test: DDS rubric v3 through the pipeline`.**
- `test/integration/dds_rubric_v3_test.go`: api и worker с `ASSESSMENT_JUDGE=llm` на подменённом судье (HTTP-заглушка OpenAI-совместимого API, как в интеграционных тестах `descjudge`, если они есть, иначе `httptest`). Проверяются:
  - занятие на `dds-district-tree-cycle-01-v2` фиксирует `dds/rubric-v3`;
  - после закрытия `semantic_input` содержит оба запроса;
  - `auto rev=1 ready` с частичным `D_COMMENT_CONTENT` и числом ошибок;
  - судья отвечает `needs_review` → оценка `needs_review`, `score=NULL`;
  - судья недоступен до исчерпания попыток → `needs_review` через финализатор;
  - `ASSESSMENT_JUDGE=off` → занятие на v2, модель не вызывается.
- Существующие тесты ДДС на v2 не ослабляются: при `off` они проходят без изменений.
- e2e (`compose.no-llm.yaml`, судья выключен): разбор ДДС не меняется.
- Проверка: `make test-integration`, `cd web && npm run test:e2e`.

**c10 — `docs: DDS-4 complete`.**
- `CLAUDE.md` (текущее состояние, карта кода: `commentjudge`), `slice-planning-dds.md` (ДДС-4 → реализован), `README.md`/`seed/README.md`, если поведение видно пользователю, `LOG.MD`.
- Живая проверка на локальной модели (`make model`, `docker compose up`): пройти три цикла и отказ с хорошими и плохими комментариями, записать долю совпадений `live_test` и время ответа судьи в `LOG.MD`.

## Verification

- `go test ./...` по затронутым пакетам на каждом коммите с кодом.
- `python3 design-docs/contracts/check.py` после c2.
- `make verify`, `make verify-web`, `cd web && npm run lint`.
- `make test-integration` на c9.
- `cd web && npm run test:e2e`.
- Вручную: с включённым судьёй пройти `dds-district-tree-cycle-01-v2`, в «Проведение работ» не упомянуть автовышку и сделать две опечатки → `D_COMMENT_CONTENT` `partial`, `G_GRAMMAR` `partial` со списком исправлений, колонка ошибок в отчёте и CSV.

## Риски

- Точность судьи на телеграфных комментариях ДДС не измерена. Прототип 112 (94,8%) проверялся на развёрнутых описаниях. До c10 точность известна только по `live_test` на своей разметке.
- Грамматика на малой локальной модели может давать ложные срабатывания на сокращениях. Поэтому у `G_GRAMMAR` малый вес, `fragment` проверяется по тексту, а исправление всегда видно преподавателю в разборе.
- Автоматическая формулировка вопроса из факта («Комментарий сообщает: …») может оказаться хуже ручной. Если `live_test` покажет проблему, в схему добавляется необязательный `comment_questions` с ручными формулировками — это новая версия промпта, а не переделка движка.
- Нагрузка: у карточки ДДС v3 до двух вызовов модели в той же очереди `llm`, что и у судьи 112 и ИИ-заявителя. Ёмкость по-прежнему не измерена (W0 — техдолг ДДС-10).
