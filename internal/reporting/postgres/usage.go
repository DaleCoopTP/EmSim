package postgres

import (
	"context"
	"time"

	"emsim/internal/reporting"
)

var _ reporting.UsageStore = (*Store)(nil)

func (s *Store) Usage(ctx context.Context, from, to time.Time) (reporting.Usage, error) {
	u := reporting.Usage{From: from, To: to, Days: []reporting.UsageDay{}, ByRole: []reporting.UsageByRole{}, ByExercise: []reporting.UsageByExercise{}}
	rows, err := s.pool.Query(ctx, `
WITH days AS (SELECT d::date AS day FROM generate_series($1::timestamptz, $2::timestamptz - interval '1 day', interval '1 day') d),
lg AS (SELECT (at AT TIME ZONE 'UTC')::date AS day, count(*) AS n FROM audit_log
        WHERE action = 'auth.login' AND outcome = 'ok' AND at >= $1 AND at < $2 GROUP BY 1),
au AS (SELECT (at AT TIME ZONE 'UTC')::date AS day, count(DISTINCT actor_id) AS n FROM audit_log
        WHERE actor_id IS NOT NULL AND outcome = 'ok' AND at >= $1 AND at < $2 GROUP BY 1),
ls AS (SELECT (started_at AT TIME ZONE 'UTC')::date AS day, count(*) AS n FROM lessons
        WHERE mode <> 'preview' AND started_at >= $1 AND started_at < $2 GROUP BY 1),
le AS (SELECT (stopped_at AT TIME ZONE 'UTC')::date AS day, count(*) AS n FROM lessons
        WHERE mode <> 'preview' AND stopped_at >= $1 AND stopped_at < $2 GROUP BY 1),
ic AS (SELECT (i.closed_at AT TIME ZONE 'UTC')::date AS day, count(*) AS n FROM items i
        JOIN runs r ON r.id = i.run_id JOIN lessons l ON l.id = r.lesson_id
        WHERE l.mode <> 'preview' AND i.state = 'closed' AND i.closed_at >= $1 AND i.closed_at < $2 GROUP BY 1),
aa AS (SELECT (a.created_at AT TIME ZONE 'UTC')::date AS day,
              count(*) FILTER (WHERE a.kind = 'auto') AS auto, count(*) FILTER (WHERE a.kind = 'expert') AS expert,
              count(*) FILTER (WHERE a.kind = 'auto' AND a.model IS NOT NULL AND a.model <> '') AS judged
         FROM assessments a JOIN items i ON i.id = a.item_id JOIN runs r ON r.id = i.run_id JOIN lessons l ON l.id = r.lesson_id
        WHERE l.mode <> 'preview' AND a.created_at >= $1 AND a.created_at < $2 GROUP BY 1),
cr AS (SELECT (terminal_at AT TIME ZONE 'UTC')::date AS day, count(*) AS n FROM tasks
        WHERE kind = 'caller.reply' AND status = 'done' AND terminal_at >= $1 AND terminal_at < $2 GROUP BY 1)
SELECT days.day::text, COALESCE(lg.n,0), COALESCE(au.n,0), COALESCE(ls.n,0), COALESCE(le.n,0), COALESCE(ic.n,0),
       COALESCE(aa.auto,0), COALESCE(aa.expert,0), COALESCE(cr.n,0), COALESCE(aa.judged,0)
FROM days LEFT JOIN lg USING (day) LEFT JOIN au USING (day) LEFT JOIN ls USING (day) LEFT JOIN le USING (day)
          LEFT JOIN ic USING (day) LEFT JOIN aa USING (day) LEFT JOIN cr USING (day)
ORDER BY days.day`, from, to)
	if err != nil {
		return u, err
	}
	for rows.Next() {
		var d reporting.UsageDay
		if err := rows.Scan(&d.Day, &d.Logins, &d.ActiveUsers, &d.LessonsStarted, &d.LessonsStopped, &d.CardsClosed, &d.AutoAssessments, &d.ExpertAssessments, &d.CallerReplies, &d.JudgeCalls); err != nil {
			rows.Close()
			return u, err
		}
		u.Days = append(u.Days, d)
		t := &u.Totals
		t.Logins += d.Logins
		t.LessonsStarted += d.LessonsStarted
		t.LessonsStopped += d.LessonsStopped
		t.CardsClosed += d.CardsClosed
		t.AutoAssessments += d.AutoAssessments
		t.ExpertAssessments += d.ExpertAssessments
		t.CallerReplies += d.CallerReplies
		t.JudgeCalls += d.JudgeCalls
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return u, err
	}
	u.Totals.Day = "total"

	if err := s.pool.QueryRow(ctx, `SELECT count(DISTINCT actor_id) FROM audit_log WHERE actor_id IS NOT NULL AND outcome = 'ok' AND at >= $1 AND at < $2`, from, to).Scan(&u.Totals.ActiveUsers); err != nil {
		return u, err
	}
	roleRows, err := s.pool.Query(ctx, `
SELECT actor_role, count(*) FILTER (WHERE action = 'auth.login'), count(DISTINCT actor_id)
FROM audit_log WHERE actor_id IS NOT NULL AND actor_role <> '' AND outcome = 'ok' AND at >= $1 AND at < $2
GROUP BY actor_role ORDER BY actor_role`, from, to)
	if err != nil {
		return u, err
	}
	for roleRows.Next() {
		var r reporting.UsageByRole
		if err := roleRows.Scan(&r.Role, &r.Logins, &r.ActiveUsers); err != nil {
			roleRows.Close()
			return u, err
		}
		u.ByRole = append(u.ByRole, r)
	}
	roleRows.Close()
	if err := roleRows.Err(); err != nil {
		return u, err
	}
	exRows, err := s.pool.Query(ctx, `
SELECT t, sum(started), sum(stopped), sum(closed) FROM (
  SELECT exercise_type AS t, count(*) FILTER (WHERE started_at >= $1 AND started_at < $2) AS started,
         count(*) FILTER (WHERE stopped_at >= $1 AND stopped_at < $2) AS stopped, 0 AS closed
    FROM lessons WHERE mode <> 'preview' GROUP BY 1
  UNION ALL
  SELECT i.exercise_type, 0, 0, count(*) FROM items i JOIN runs r ON r.id = i.run_id JOIN lessons l ON l.id = r.lesson_id
   WHERE l.mode <> 'preview' AND i.state = 'closed' AND i.closed_at >= $1 AND i.closed_at < $2 GROUP BY 1
) x GROUP BY t ORDER BY t`, from, to)
	if err != nil {
		return u, err
	}
	for exRows.Next() {
		var e reporting.UsageByExercise
		if err := exRows.Scan(&e.ExerciseType, &e.LessonsStarted, &e.LessonsStopped, &e.CardsClosed); err != nil {
			exRows.Close()
			return u, err
		}
		u.ByExercise = append(u.ByExercise, e)
	}
	exRows.Close()
	return u, exRows.Err()
}
