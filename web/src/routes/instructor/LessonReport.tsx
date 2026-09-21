import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import { errorMessage } from "../../api/errors";
import {
  reportFilesQueryKey, requestLessonPDF, useLessonReport, useReportFiles,
  type AssessmentStatus, type ReportFile,
} from "../../api/reporting";
import { formatDateTime } from "../../format";

const assessmentLabels: Record<AssessmentStatus, string> = {
  ready: "Готова",
  needs_review: "Нужна проверка",
  unavailable: "Недоступна",
  pending: "В обработке",
  not_assessed: "Не оценивается",
};

const reportStatusLabels: Record<ReportFile["status"], string> = {
  queued: "В очереди",
  building: "Формируется",
  ready: "Готов",
  failed: "Не сформирован",
};

function score(value: number | null) {
  return value === null ? "—" : value.toFixed(1);
}

function seconds(value: number | null) {
  return value === null ? "—" : `${value.toFixed(1)} с`;
}

export function LessonReportRoute() {
  const { lessonId = "" } = useParams();
  const report = useLessonReport(lessonId);
  const files = useReportFiles(lessonId);
  const queryClient = useQueryClient();
  const requestPDF = useMutation({
    mutationFn: () => requestLessonPDF(lessonId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: reportFilesQueryKey(lessonId) }),
  });

  if (report.isPending) return <p>Загрузка отчёта…</p>;
  if (report.isError) return <p className="error">{errorMessage(report.error)}</p>;
  const value = report.data;

  return (
    <section>
      <p><Link to={`/instructor/lessons/${lessonId}`}>← К занятию</Link></p>
      <h1>Отчёт: {value.lesson.title}</h1>
      <p>Завершено: {formatDateTime(value.lesson.finished_at)}. В расчёты попадают только готовые оценки; вводные карточки не оцениваются.</p>

      <h2>Итоги</h2>
      <dl>
        <dt>Средний балл</dt><dd>{score(value.aggregates.avg_score)}</dd>
        <dt>Готовых оценок</dt><dd>{value.aggregates.ready_assessments}</dd>
        <dt>Ожидают оценки</dt><dd>{value.aggregates.pending_assessments}</dd>
        <dt>Прервано карточек</dt><dd>{value.aggregates.interrupted_items}</dd>
      </dl>
      {value.aggregates.score_histogram.length > 0 && (
        <table>
          <thead><tr><th>Диапазон баллов</th><th>Карточек</th></tr></thead>
          <tbody>{value.aggregates.score_histogram.map((bucket) => <tr key={bucket.bucket}><td>{bucket.bucket}</td><td>{bucket.count}</td></tr>)}</tbody>
        </table>
      )}

      <h2>Участники</h2>
      <table>
        <thead><tr><th>ФИО</th><th>РМ</th><th>Карточек</th><th>Средний балл</th><th>Ожидают оценки</th><th>Прервано</th></tr></thead>
        <tbody>{value.participants.map((participant) => (
          <tr key={participant.user_id}>
            <td>{participant.full_name}</td><td>№ {participant.workstation_no}</td><td>{participant.items}</td>
            <td>{score(participant.avg_score)}</td><td>{participant.pending_assessments}</td><td>{participant.interrupted_items}</td>
          </tr>
        ))}</tbody>
      </table>

      <h2>Карточки</h2>
      <table>
        <thead><tr><th>Обучаемый</th><th>РМ</th><th>Сценарий</th><th>Карточка</th><th>Балл</th><th>Оценка</th><th>Время</th><th>Ошибки</th></tr></thead>
        <tbody>{value.items.map((item) => (
          <tr key={item.item_id}>
            <td>{item.full_name}</td><td>№ {item.workstation_no}</td><td>{item.scenario_title}</td><td>{item.card_number}</td>
            <td>{score(item.score)}</td><td>{assessmentLabels[item.assessment_status]}</td>
            <td>{seconds(item.total_seconds)}</td><td>{item.errors.map((error) => error.label).join("; ") || "—"}</td>
          </tr>
        ))}</tbody>
      </table>

      <h2>Экспорт</h2>
      <p><a href={`/api/v1/lessons/${encodeURIComponent(lessonId)}/report.csv`}>Скачать CSV</a></p>
      <p><button type="button" disabled={requestPDF.isPending} onClick={() => requestPDF.mutate()}>Сформировать PDF-снимок</button></p>
      {requestPDF.isError && <p role="alert" className="error">{errorMessage(requestPDF.error)}</p>}
      {files.isError && <p role="alert" className="error">{errorMessage(files.error)}</p>}
      {files.data && files.data.length > 0 && (
        <table>
          <thead><tr><th>Запрошен</th><th>Состояние</th><th>Готов</th><th>Файл</th></tr></thead>
          <tbody>{files.data.map((file) => (
            <tr key={file.id}>
              <td>{formatDateTime(file.requested_at)}</td><td>{reportStatusLabels[file.status]}</td><td>{formatDateTime(file.generated_at)}</td>
              <td>{file.download_url ? <a href={file.download_url}>Скачать PDF</a> : "—"}</td>
            </tr>
          ))}</tbody>
        </table>
      )}
    </section>
  );
}
