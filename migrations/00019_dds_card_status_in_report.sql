-- ДДС-3/ADR-032: the reporting rows need items.reaction and exercise_type
-- to derive a DDS row's own card status (Зарегистрирована/Не оповещено/
-- В работе/Отказ/Завершена/Не завершено, ADR-030's dds.CardStatusOf) —
-- an operator112_intake row simply ignores the extra column.
-- +goose Up
DROP VIEW lesson_report_rows;
CREATE VIEW lesson_report_rows AS
SELECT l.id AS lesson_id, l.title AS lesson_title, l.mode AS lesson_mode, l.state AS lesson_state,
       r.user_id, u.full_name, w.number AS workstation_no, r.level_at_start AS level, r.exercise_type,
       i.id AS item_id, i.ordinal, i.state AS item_state, i.reaction, i.close_reason, i.offered_at, i.opened_at, i.closed_at, i.interruptions,
       i.card->>'number' AS card_number, sv.scenario_id, s.title AS scenario_title, sv.version AS scenario_version, sv.difficulty,
       (ev.body->'derived'->>'open_seconds')::numeric AS open_seconds,
       (ev.body->'derived'->>'primary_seconds')::numeric AS primary_seconds,
       (ev.body->'derived'->>'work_seconds')::numeric AS work_seconds,
       (ev.body->'derived'->>'total_seconds')::numeric AS total_seconds,
       fa.assessment_id, fa.revision AS assessment_revision, fa.kind AS assessment_kind, fa.status AS assessment_status,
       fa.score, fa.passed, fa.critical_errors, a.criteria, a.feedback
FROM lessons l
JOIN runs r ON r.lesson_id = l.id
JOIN users u ON u.id = r.user_id
JOIN workstations w ON w.id = r.workstation_id
JOIN items i ON i.run_id = r.id
JOIN scenario_versions sv ON sv.id = i.scenario_version_id
JOIN scenarios s ON s.id = sv.scenario_id
LEFT JOIN evidence ev ON ev.item_id = i.id
LEFT JOIN item_final_assessment fa ON fa.item_id = i.id
LEFT JOIN assessments a ON a.id = fa.assessment_id;

-- +goose Down
DROP VIEW lesson_report_rows;
CREATE VIEW lesson_report_rows AS
SELECT l.id AS lesson_id, l.title AS lesson_title, l.mode AS lesson_mode, l.state AS lesson_state,
       r.user_id, u.full_name, w.number AS workstation_no, r.level_at_start AS level, r.exercise_type,
       i.id AS item_id, i.ordinal, i.state AS item_state, i.close_reason, i.offered_at, i.opened_at, i.closed_at, i.interruptions,
       i.card->>'number' AS card_number, sv.scenario_id, s.title AS scenario_title, sv.version AS scenario_version, sv.difficulty,
       (ev.body->'derived'->>'open_seconds')::numeric AS open_seconds,
       (ev.body->'derived'->>'primary_seconds')::numeric AS primary_seconds,
       (ev.body->'derived'->>'work_seconds')::numeric AS work_seconds,
       (ev.body->'derived'->>'total_seconds')::numeric AS total_seconds,
       fa.assessment_id, fa.revision AS assessment_revision, fa.kind AS assessment_kind, fa.status AS assessment_status,
       fa.score, fa.passed, fa.critical_errors, a.criteria, a.feedback
FROM lessons l
JOIN runs r ON r.lesson_id = l.id
JOIN users u ON u.id = r.user_id
JOIN workstations w ON w.id = r.workstation_id
JOIN items i ON i.run_id = r.id
JOIN scenario_versions sv ON sv.id = i.scenario_version_id
JOIN scenarios s ON s.id = sv.scenario_id
LEFT JOIN evidence ev ON ev.item_id = i.id
LEFT JOIN item_final_assessment fa ON fa.item_id = i.id
LEFT JOIN assessments a ON a.id = fa.assessment_id;
