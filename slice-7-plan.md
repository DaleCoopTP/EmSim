# Срез 7 — план реализации: отчёты, история, CSV и PDF

Контракт: `slice-planning.md` §8, RFC-001 §4.2/§4.3, ADR-006/014/020 и `design-docs/contracts/{openapi.yaml,tasks.schema.json,schema.sql}`.

## Цель

Завершённое занятие получает доступный преподавателю отчёт по участникам и карточкам, синхронный CSV и неизменяемые PDF-артефакты. Обучаемый видит только собственную историю, текущий уровень и безопасные ошибки. STT/LLM, рекомендации уровня, общегрупповой прогресс и авторинг сценариев в срез не входят.

## Решения

- Только `finished` занятие может быть прочитано и экспортировано; draft/running/stopped возвращают `lesson_not_finished`.
- JSON и CSV читают текущую финальную ревизию. PDF фиксирует полный `LessonReport` и provenance (`assessment_id`/`revision`) в `report_files.basis` в момент запроса. Повторный запрос создаёт новый артефакт и никогда не заменяет старый.
- `score=NULL` остаётся отсутствующей оценкой. Итоговая строка всегда несёт независимые `item_state`, `assessment_status` и `assessment_kind`; в агрегаты баллов входят только `assessment_status=ready`.
- Intro-карточки видимы в истории как `not_assessed` и исключены из score/error aggregates.
- Безопасная ошибка обучаемого содержит только allowlist id/локализованное название критерия/status/guide_ref. Эталон, evidence, rubrics, expert reason и внутренние explanations не выдаются.
- CSV: UTF-8 BOM, `;`, CRLF, quoted fields и apostrophe-prefix у ячеек, начинающихся с `=+-@`.
- `report.build` использует отдельный worker pool `report` (`REPORT_CONCURRENCY=1`), чтобы PDF не задерживал `lesson.close`.

## API

- `GET /lessons/{lessonId}/report`, `GET /lessons/{lessonId}/report.csv` — instructor-владелец finished-занятия.
- `POST /lessons/{lessonId}/report.pdf`, `GET /lessons/{lessonId}/report-files`, `GET /reports/{reportId}/download` — создание, наблюдение и скачивание PDF-артефактов.
- `GET /my/results`, `GET /my/progress` — self-only trainee projections.

## Коммиты

1. contracts/ADR; 2. reporting schema; 3. read projections; 4. HTTP JSON/CSV; 5. PDF renderer; 6. report worker/artifact endpoints; 7. instructor UI; 8. trainee UI; 9. end-to-end acceptance and documentation.

