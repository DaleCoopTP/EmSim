import { Link, useParams } from "react-router-dom";
import { errorMessage } from "../../api/errors";
import { useLessonAssessments } from "../../api/assessment";
import { useLesson } from "../../api/training";
import { formatDateTime } from "../../format";

const statusLabel: Record<string, string> = { waiting: "ожидает подготовки", pending: "в очереди", leased: "оценивается", done: "готово", failed: "ошибка", dead_letter: "ошибка", cancelled: "отменена" };

export function LessonAssessmentsRoute() {
  const { lessonId = "" } = useParams();
  const rows = useLessonAssessments(lessonId);
  const lesson = useLesson(lessonId);
  if (rows.isPending || lesson.isPending) return <p>Загрузка…</p>;
  if (rows.isError) return <p className="error">{errorMessage(rows.error)}</p>;
  if (lesson.isError) return <p className="error">{errorMessage(lesson.error)}</p>;
  const intake = lesson.data.exercise_type === "operator112_intake";
  return <section>
    <p><Link to={`/instructor/lessons/${lessonId}`}>← К занятию</Link></p>
    <h1>Разбор карточек</h1>
    {rows.data.length === 0 ? <p>Закрытых карточек пока нет.</p> : <table>
      <thead><tr><th>РМ</th><th>Обучаемый</th><th>Карточка</th><th>Закрыта</th><th>{intake ? "Ручная оценка" : "Автооценка"}</th><th>Итог</th><th /></tr></thead>
      <tbody>{rows.data.map((row) => <tr key={row.item_id}>
        <td>№ {row.workstation_no}</td><td>{row.user.full_name}</td><td>№ {row.card_number || row.ordinal}</td><td>{formatDateTime(row.closed_at)}</td>
		<td>{intake ? row.final ? "Выставлена" : "Ожидает преподавателя" : row.automatic_state ? (statusLabel[row.automatic_state] ?? row.automatic_state) : "нет (вводный режим)"}</td>
        <td>{row.final ? `${row.final.status}${row.final.score == null ? "" : ` · ${row.final.score.toFixed(1)}`}` : "—"}</td>
        <td><Link to={`/instructor/items/${row.item_id}/review`}>Открыть →</Link></td>
      </tr>)}</tbody>
    </table>}
  </section>;
}
