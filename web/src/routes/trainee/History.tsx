import { Link } from "react-router-dom";
import { errorMessage } from "../../api/errors";
import { useMyProgress, useMyResults, type AssessmentStatus } from "../../api/reporting";
import { formatDateTime } from "../../format";

const assessmentLabels: Record<AssessmentStatus, string> = {
  ready: "Готова",
  needs_review: "Нужна проверка",
  unavailable: "Недоступна",
  pending: "В обработке",
  not_assessed: "Не оценивается",
};

function score(value: number | null) {
  return value === null ? "—" : value.toFixed(1);
}

export function HistoryRoute() {
  const progress = useMyProgress();
  const results = useMyResults();

  if (progress.isPending || results.isPending) return <p>Загрузка истории…</p>;
  if (progress.isError) return <p className="error">{errorMessage(progress.error)}</p>;
  if (results.isError) return <p className="error">{errorMessage(results.error)}</p>;

  return (
    <section>
      <h1>Моя история</h1>
      <p>Здесь показаны только ваши завершённые карточки. Эталонные ответы и материалы проверки не раскрываются.</p>

      <h2>Прогресс</h2>
      <dl>
        <dt>Текущий уровень</dt><dd>{progress.data.level}</dd>
        <dt>Завершено карточек</dt><dd>{progress.data.completed_items}</dd>
        <dt>Средний балл</dt><dd>{score(progress.data.avg_score)}</dd>
        <dt>Ожидают оценки</dt><dd>{progress.data.pending_assessments}</dd>
      </dl>
      {progress.data.by_lesson.length > 0 && (
        <table>
          <thead><tr><th>Занятие</th><th>Дата</th><th>Карточек</th><th>Средний балл</th></tr></thead>
          <tbody>{progress.data.by_lesson.map((lesson) => (
            <tr key={lesson.lesson_id}><td>{lesson.title}</td><td>{formatDateTime(lesson.date)}</td><td>{lesson.items}</td><td>{score(lesson.avg_score)}</td></tr>
          ))}</tbody>
        </table>
      )}
      {progress.data.error_frequency.length > 0 && (
        <p>Частые ошибки: {progress.data.error_frequency.map((error) => `${error.criterion_id} (${error.count})`).join(", ")}.</p>
      )}

      <h2>Карточки</h2>
      {results.data.length === 0 ? <p>Завершённых карточек пока нет.</p> : (
        <table>
          <thead><tr><th>Занятие</th><th>Карточка</th><th>Завершена</th><th>Балл</th><th>Статус оценки</th><th>Безопасная обратная связь</th></tr></thead>
          <tbody>{results.data.map((item) => (
            <tr key={item.item_id}>
              <td>{item.lesson_title}</td><td>{item.card_number}</td><td>{formatDateTime(item.closed_at)}</td>
              <td>{score(item.score)}</td><td>{assessmentLabels[item.assessment_status]}</td>
              <td>{item.errors.length === 0 ? "—" : item.errors.map((error) => (
                <span key={error.criterion_id}>{error.label}{error.guide_ref ? ` (${error.guide_ref})` : ""}; </span>
              ))}</td>
            </tr>
          ))}</tbody>
        </table>
      )}
      <p><Link to="/my">Вернуться к рабочему месту</Link></p>
    </section>
  );
}
