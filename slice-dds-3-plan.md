# План среза ДДС-3: рубрика ДДС v2 (детерминированная)

Дата: 2026-09-28. Статус: план → реализация в этой ветке (`dds-2`, продолжение после ДДС-2).

## Контекст

См. [ADR-032](design-docs/adr/032-dds-rubric-v2.md) для полного решения и обоснования. Коротко: `dds/rubric-v1` не соответствует роли ДДС по ADR-030/031 (правка карточки, адрес и журнал звонка больше не в объёме; реакция на доклады бригады ещё не оценивается). ДДС-3 добавляет `dds/rubric-v2`, `v1` остаётся для уже зафиксированных занятий.

Решения пользователя 28.09 — см. ADR-032 «Контекст».

## Коммиты

**c1 — `docs: DDS-3 plan and ADR-032 (DDS rubric v2)`.**
- `design-docs/adr/032-dds-rubric-v2.md`, `design-docs/adr/README.md`, этот план, `LOG.MD`.
- Проверка: `python3 design-docs/contracts/check.py`.

**c2 — `contracts: dds/rubric-v2, reference.required_contacts, optional call log`.**
- Новый `design-docs/contracts/rubric.dds.v2.json` (ADR-032: 7 критериев, `version: "dds/rubric-v2"`). `rubric.default.json` (v1) не меняется.
- `scenario.schema.json`/`scenario-file.schema.json`: `$defs.reference.required_contacts: string[]` (ключи существующих контактов).
- `openapi.yaml`: у `call_end` `accepted_by`/`summary` описаны как допустимо пустые для карточек служб с непустым `terminal`; краткое описание новых критериев рубрики, если рубрика описана в контракте отдельно.
- `design-docs/contracts/embed.go`: `rubric.dds.v2.json` в `//go:embed`.
- `check.py`: новый rubric в `schemas`/`examples`/`pairs`; проверка, что v2 не содержит `T_COMPLETE`/`D_FIELD_CORRECTIONS`/`C_CALL_LOG*`/`G_ADDRESS`/`C_CALL_CONTENT`, но содержит `T_PROGRESS`/`C_CALLS`; сумма весов v2 = 100.
- Проверка: `python3 design-docs/contracts/check.py`.

**c3 — `content: freeze dds/rubric-v2 for new DDS lessons; validate required_contacts`.**
- `internal/content/rubric.go`: `RubricVersionFor(ExerciseTypeDDSProcessing)` возвращает `dds/rubric-v2`; `rubricCriterionIDs` — объединение id из `rubric.default.json` и `rubric.dds.v2.json` (по образцу `operator112RubricCriterionIDs`).
- `internal/content/body.go`: `Reference.RequiredContacts []string`.
- `internal/content/validate.go`: `validateRequiredContacts` — каждый ключ существует в `contacts[]`; вызывается из `Validate` рядом с `validateCall`.
- Тесты: `internal/training/rubric_version_test.go` — новое занятие ДДС фиксирует `dds/rubric-v2`; занятие, уже зафиксированное на `dds/rubric-v1`, не пересчитывается при повторной загрузке рубрики.
- Проверка: `go test ./internal/content/... ./internal/training/...`.

**c4 — `assessment/dds: T_PROGRESS, report-aware S_SEQUENCE, C_CALLS`.**
- `internal/assessment/rubric.go`: `loadedDDSv2 = sync.OnceValues(...)`; `LoadRubric(dds_processing, "dds/rubric-v2")`; `LoadDefaultFor(dds_processing)` возвращает v2.
- `internal/assessment/dds/progress.go` (новый файл): общий разбор «доклад → момент, когда его услышали» (по `EvidenceEvent`+`EvidenceCall`, аналог `training/dds.ReportReactions`, но над `training.EvidenceBody`, без импорта пакета `training/dds`, чтобы не создавать цикл через `training`); `t_progress`, `s_sequence_reports`, `c_calls`.
- `internal/assessment/dds/rules.go`: ветки `case "t_progress"`, `case "s_sequence_reports"`, `case "c_calls"` в `evaluateCriterion`; поправка `critical_when=refused_profile_incident` — также для `completed_without_team`.
- Тесты в `progress_test.go`: доклад вовремя; доклад с опозданием; статус раньше доклада (штраф только в `S_SEQUENCE`, `T_PROGRESS` для этого пункта met); пропущенный входящий (`T_PROGRESS` без этого пункта, `C_CALLS` не полный балл); бригаде не звонили (`T_PROGRESS` not_met на все пункты); stop в середине цикла (`not_applicable` для ещё не наступивших пунктов); верное «Не принята» (все три — `not_applicable`); 03 `completed_without_team` вместо `accepted` — критично; `serverInterrupted`.
- Проверка: `go test ./internal/assessment/...`.

**c5 — `training/dds: call log optional on outgoing calls of terminal-workflow services`.**
- `internal/training/dds/rules.go` `decideCallEnd`: пустые `accepted_by`/`summary` допустимы для исходящего звонка, если `item`'s снимок workflow (`terminal`) непуст; иначе поведение не меняется.
- Тест в `terminal_rules_test.go`.
- Проверка: `go test ./internal/training/...`.

**c6 — `reporting: DDS v2 criteria labels and card status`.**
- `internal/reporting/domain.go`: подписи `T_PROGRESS` («Реакция на доклады бригады») и `C_CALLS` («Обязательная связь»); старые `C_CALL_MADE`/`C_CALL_LOG`/`G_ADDRESS` подписи не удаляются (нужны для v1-занятий).
- Статус карточки (`dds.CardStatusOf`) — колонка в строках отчёта, CSV, PDF, если такой колонки ещё нет для законченных занятий.
- Тесты: `csv_test.go`, `pdf_test.go`.
- Проверка: `go test ./internal/reporting/...`.

**c7 — `web: DDS v2 review labels and optional call log`.**
- `web/src/routes/instructor/ItemReview.tsx` `CriteriaTable`: название критерия берётся из `rubric_effective` (`rubricByID`), а не из голого id, для обоих рубрик ДДС.
- `web/src/routes/instructor/*` отчёт занятия: колонка статуса карточки, если её ещё нет.
- `web/src/routes/trainee/Workplace.tsx`: «Кто принял»/«Суть сообщения» — необязательные подписи на картах с финальными статусами (нет required/звёздочки).
- `npm run gen`/`schema.d.ts`, если менялась OpenAPI.
- Проверка: `make verify-web`, `cd web && npm run lint`.

**c8 — `test: DDS rubric v2 through the pipeline`.**
- `test/integration/assessment_pipeline_test.go` (или новый файл): занятие на `dds-district-tree-cycle-01-v2` закрывается с `auto rev=1 ready` по `dds/rubric-v2`, с непустыми `T_PROGRESS`/`C_CALLS`/`S_SEQUENCE`; занятие, зафиксированное на v1 (создано до релиза — через прямую вставку `lessons.rubric_version`), продолжает оцениваться по v1.
- Существующие интеграционные тесты, ожидающие `dds/rubric-v1` у нового занятия, поправляются на v2 без ослабления самих проверок.
- e2e: разбор после прохождения `dds-district-tree-cycle-01-v2` показывает «Реакция на доклады бригады».
- Проверка: `make test-integration`, `cd web && npm run test:e2e` (или ручной прогон, если Docker недоступен — см. риски ДДС-2).

**c9 — `docs: DDS-3 complete`.**
- `CLAUDE.md` (текущее состояние), `slice-planning-dds.md` (статус ДДС-3 → реализован), `LOG.MD`; `seed/README.md`/`README.md`, если поведение видимо пользователю.

## Verification

- `go test ./...` (минимум затронутые пакеты) при каждом коммите с кодом.
- `python3 design-docs/contracts/check.py` после c2.
- `make verify`, `make verify-web`, `cd web && npm run lint`.
- `make test-integration` на c8.
- `cd web && npm run test:e2e`.
- Вручную: пройти `dds-district-tree-cycle-01-v2` с одним пропущенным входящим и одним ранним статусом; проверить `C_CALLS` и `S_SEQUENCE` частичные, `T_PROGRESS` без пропущенного пункта, отчёт/CSV со статусом карточки.
