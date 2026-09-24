import { useQueryClient } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import { errorMessage } from "../../api/errors";
import { useEventStream } from "../../api/realtime";
import { monitorQueryKey, useMonitor, type MonitorRow } from "../../api/training";
import { formatDateTime } from "../../format";
import { reactionLabel } from "../../labels";

const commandLabels: Record<string, string> = {
  open: "Открыл карточку",
  set_status: "Сменил статус",
  add_comment: "Добавил комментарий",
  set_card_field: "Исправил поле",
  close: "Закрыл карточку",
  control_report: "Сообщение после закрытия",
  answer_incoming: "Принял входящий вызов",
  end_incoming: "Завершил разговор",
  save_intake_draft: "Сохранил карточку",
  add_incident_type: "Добавил тип происшествия",
  remove_incident_type: "Убрал тип происшествия",
  review_service_selection: "Зафиксировал выбор служб",
  complete_profile_case: "Завершил кейс",
  dispatch_intake: "Направил карточку",
  complete_intake: "Завершил обработку",
  notify_services: "Оповестил службы",
  hold_incoming: "Поставил на удержание",
  resume_incoming: "Вернулся к разговору",
  ask_intake_question: "Задал уточняющий вопрос",
  mark_no_contact: "Закрыл: нет контакта",
  mark_call_dropped: "Закрыл: срыв звонка",
  send_caller_message: "Сообщение заявителю",
};

// RFC-001 §7.7's monitor: SSE (lessons/{id}/stream) is an invalidation
// signal only — every render's actual data comes from GET .../monitor,
// re-read on stream.ready/resync/invalidate and on the usual TanStack
// fallbacks (focus/reconnect).
export function MonitorRoute() {
  const { lessonId = "" } = useParams();
  const monitor = useMonitor(lessonId);
  const queryClient = useQueryClient();

  useEventStream(`/lessons/${encodeURIComponent(lessonId)}/stream`, () => {
    void queryClient.invalidateQueries({ queryKey: monitorQueryKey(lessonId) });
  });

  if (monitor.isPending) return <p>Загрузка…</p>;
  if (monitor.isError) return <p className="error">{errorMessage(monitor.error)}</p>;
  const data = monitor.data;

  return (
    <section className="instructor-page monitor-page">
      <p className="back-link"><Link to={`/instructor/lessons/${lessonId}`}>← К занятию</Link></p>
      <header className="page-heading monitor-heading">
        <div><h1>Монитор: {data.lesson.title}</h1><p>Обновляется по событиям сервера · {formatDateTime(data.server_time)}</p></div>
        <span className={`status-badge lesson-state-${data.lesson.state}`}>{data.lesson.state === "running" ? "идёт занятие" : data.lesson.state}</span>
      </header>
      {data.lesson.state === "stopped" && <p className="notice">Занятие остановлено, карточки закрываются фоновой задачей.</p>}
      <p className="monitor-review-link"><Link to={`/instructor/lessons/${lessonId}/assessments`}>Перейти к разбору карточек →</Link></p>
      {data.rows.length === 0 ? (
        <p>Пока никто не начал.</p>
      ) : (
        <div className="arm-table-wrap"><table className="monitor-table">
          <thead>
            <tr><th>РМ</th><th>Обучаемый</th><th>Онлайн</th><th>Открытые карточки</th><th>Осталось</th><th>Готово</th><th>Последнее действие</th></tr>
          </thead>
          <tbody>{data.rows.map((row) => <MonitorRowView key={row.run_id} row={row} />)}</tbody>
        </table></div>
      )}
    </section>
  );
}

function MonitorRowView({ row }: { row: MonitorRow }) {
  return (
    <tr className={row.online ? "monitor-online" : "monitor-offline"}>
      <td>№ {row.workstation_no}</td>
      <td>{row.user.full_name}</td>
      <td><span className={`online-indicator ${row.online ? "online" : "offline"}`}>{row.online ? "онлайн" : "нет связи"}</span></td>
      <td>
        {row.active_items.length === 0 && "—"}
        {row.active_items.map((item) => (
          <div key={item.id}>
			№ {item.card_number} · {item.exercise_type === "operator112_intake" ? item.call_status === "not_applicable" ? `кейс без разговора${item.incident_type ? ` · ${item.incident_type}` : " · тип не выбран"}${item.notified ? " · оповещено" : ""}` : `вызов ${item.call_status === "ringing" ? "ожидает" : item.call_status === "connected" ? "идёт разговор" : item.call_status === "held" ? "на удержании" : "разговор завершён"}${item.incident_type ? ` · ${item.incident_type}` : ""}${item.notified ? " · оповещено" : item.dispatched ? " · направлена" : ""}` : reactionLabel(item.reaction)}
            {item.interruptions.length > 0 && <span role="alert" className="error"> · прервана рестартом сервера</span>}
          </div>
        ))}
      </td>
      <td>{row.queue_left}</td>
      <td>{row.done}</td>
      <td>{row.last_action ? `${commandLabels[row.last_action.type] ?? row.last_action.type} · ${formatDateTime(row.last_action.server_at)}` : "—"}</td>
    </tr>
  );
}
