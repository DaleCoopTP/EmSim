# Срез 112-6, LLM-этап — ИИ-судья описания заявителя (`operator112/rubric-v3`)

Дата: 2026-09-26. Основание: решения пользователя от 26.09.2026 (ниже),
[ADR-028](design-docs/adr/028-operator112-description-llm-judge.md).
Начат до этапа 2 112-5b (замер W0) — отступление от порядка
`slice-planning-112.md`, согласованное явно.

## 1. Пользовательский результат

Обучаемый заполняет поле «Описание со слов заявителя»; после закрытия
карточки блок `DESCRIPTION_CONTENT` (10 баллов) автоматически проверяет
это описание по контрольным вопросам сценария через локальную модель.
Преподаватель видит в разборе, на какой вопрос как ответила модель, и
правит итог баллами, как любой другой критерий.

## 2. Решения пользователя (26.09.2026)

1. 10 баллов делятся поровну между вопросами сценария.
2. Любой `needs_review` (или сбой модели после всех попыток) → критерий
   `unavailable` → вся оценка `needs_review`, балл выставляет преподаватель.
3. Нет вопросов в эталоне → 0 (правило ADR-026 «эталон отсутствует»),
   модель не вызывается.
4. Вопросы — только для трёх существующих ИИ-сидов
   (car-in-water/mobile-shop/toyota-fire). Остальные 19 сценариев
   прототипа — отдельная задача.
5. Правка преподавателя — баллами за критерий целиком.
6. Новое занятие фиксирует v3 только при `ASSESSMENT_JUDGE=llm` в
   конфигурации, иначе — v2.
7. Живая проверка на Ollama — самостоятельно, сервер останавливается
   после проверки.

## 3. Коммиты

1. **c1** — этот файл, ADR-028, контракты (`rubric.operator112.v3.json`,
   `rubric.schema.json` += `card_description`, `scenario.schema.json`'s
   `description_questions`, `assessment-inputs.schema.json`'s
   `exercise_type`, `openapi.yaml`, `check.py`). ← этот коммит.
2. **c2** — исправление существующего расхождения: HTTP-слой оценки
   (`internal/assessment/http/handlers.go`) не передаёт `penalty_points`/
   `details` ни на вход, ни на выход, хотя контракт и БД их уже несут —
   панель «Подробности» никогда не заполнялась, штрафные баллы эксперта
   терялись при сохранении.
3. **c3** — `internal/content`: `Intake112DescriptionQuestion`/
   `Intake112Reference.DescriptionQuestions`, валидация, объединённый
   набор id критериев (v1+v2+v3) для `scoring`.
4. **c4** — `internal/platform/llm` (`ResponseFormat`),
   `internal/platform/config` (`ASSESSMENT_JUDGE` в api и worker,
   `JUDGE_LLM_URL`/`JUDGE_LLM_MODEL`/`JUDGE_TIMEOUT`/`JUDGE_MAX_TOKENS`).
5. **c5** — конвейер `internal/assessment`: `RuleEvaluator.Evaluate`
   получает `SemanticAnswers`; `SemanticPreparer`/`SemanticJudge`/
   `SemanticJudgeRegistry`/`JudgeConfig`; `Service.Handle` разбит на
   вызов модели вне транзакции + запись внутри неё; `LoadRubric` знает
   v3; `content.Operator112RubricVersion`.
6. **c6** — `internal/assessment/operator112`: критерий
   `DESCRIPTION_CONTENT`, `PrepareSemantic`; новый пакет `descjudge`
   (промпт, схема, разбор ответа, адаптер).
7. **c7** — композиция (`cmd/emsim`): судья в worker-процессе,
   `ASSESSMENT_JUDGE` в обоих процессах, выбор версии рубрики при
   создании занятия/предпросмотра; `compose.yaml`.
8. **c8** — сиды: новая версия 2 трёх сценариев с вопросами из архива.
9. **c9** — отчётность: `DESCRIPTION_CONTENT` в той же колонке, что
   `DESCRIPTION_PRESENT`.
10. **c10** — web: строка вопроса в `IntakeAutoAssessment.tsx`, статус
    `unavailable`; вкладка «Эталон» редактора — вопросы к описанию.
11. **c11** — интеграционные тесты (PostgreSQL) конвейера с судьёй.
12. **c12** — живая проверка на Ollama + документация
    (`README.md`, `seed/README.md`, `CLAUDE.md`, `slice-planning-112.md`,
    `LOG.MD`).

## 4. Definition of Done

- Судья вызывается только вне транзакции записи оценки; сбой/таймаут
  модели после исчерпания попыток задачи даёт `needs_review` без нуля.
- Занятие без `ASSESSMENT_JUDGE=llm` продолжает получать
  `operator112/rubric-v2` без изменений.
- Три сида с вопросами проходят сквозной прогон (ответ → карточка →
  оповещение → авто-оценка `DESCRIPTION_CONTENT`).
- `make verify`, `make verify-web`, `python3 design-docs/contracts/check.py`,
  `make test-integration`, `cd web && npm run test:e2e` — зелёные.

## 5. Вне объёма

19 остальных сценариев архива; правка по отдельным вопросам; LLM-оценка
разговора (`CALLER_TOPICS`); замер W0/`llama-server` в compose
(112-5b этап 2).
