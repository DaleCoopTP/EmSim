import { useQueryClient } from "@tanstack/react-query";
import { Link, useParams } from "react-router-dom";
import { errorMessage } from "../../api/errors";
import { useEventStream } from "../../api/realtime";
import { monitorQueryKey, useMonitor, type MonitorRow } from "../../api/training";
import { formatDateTime } from "../../format";
import { cardStatusAlarm, cardStatusLabel, reactionLabel } from "../../labels";

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
  call_start: "Начал звонок",
  call_end: "Завершил звонок",
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
            <tr><th>РМ</th><th>Обучаемый</th><th>Онлайн</th><th>Открытые карточки</th><th>Связь</th><th>Осталось</th><th>Готово</th><th>Последнее действие</th></tr>
          </thead>
          <tbody>{data.rows.map((row) => <MonitorRowView key={row.run_id} row={row} serverTime={data.server_time} />)}</tbody>
        </table></div>
      )}
    </section>
  );
}

function MonitorRowView({ row, serverTime }: { row: MonitorRow; serverTime: string }) {
  return (
    <tr className={row.online ? "monitor-online" : "monitor-offline"}>
      <td>№ {row.workstation_no}</td>
      <td>{row.user.full_name}</td>
      <td><span className={`online-indicator ${row.online ? "online" : "offline"}`}>{row.online ? "онлайн" : "нет связи"}</span></td>
      <td>
        {row.active_items.length === 0 && "—"}
        {row.active_items.map((item) => (
          <div key={item.id}>
			№ {item.card_number} · {item.exercise_type === "operator112_intake" ? item.call_status === "not_applicable" ? `кейс без разговора${item.incident_type ? ` · ${item.incident_type}` : " · тип не выбран"}${item.notified ? " · оповещено" : ""}` : `вызов ${item.call_status === "ringing" ? "ожидает" : item.call_status === "connected" ? "идёт разговор" : item.call_status === "held" ? "на удержании" : "разговор завершён"}${item.incident_type ? ` · ${item.incident_type}` : ""}${item.notified ? " · оповещено" : item.dispatched ? " · направлена" : ""}` : <>{reactionLabel(item.reaction)}{item.card_status && <> · <span className={`card-status-badge${cardStatusAlarm(item.card_status) ? " card-status-alarm" : ""}`}>{cardStatusLabel(item.card_status)}</span></>}</>}
            {item.interruptions.length > 0 && <span role="alert" className="error"> · прервана рестартом сервера</span>}
          </div>
        ))}
      </td>
      <td><MonitorComms reports={row.reports ?? []} serverTime={serverTime} /></td>
      <td>{row.queue_left}</td>
      <td>{row.done}</td>
      <td>{row.last_action ? `${commandLabels[row.last_action.type] ?? row.last_action.type} · ${formatDateTime(row.last_action.server_at)}` : "—"}</td>
    </tr>
  );
}

type MonitorReport = NonNullable<MonitorRow["reports"]>[number];

function seconds(from: string, to: string): number {
  return Math.max(0, Math.round((new Date(to).getTime() - new Date(from).getTime()) / 1_000));
}

// MonitorComms is ADR-031's crew-report view: the latest report on the
// trainee's open cards and how long the trainee took to react to it (the
// first saved status after it reached them), plus missed incoming calls.
// It shows the actual delay only; judging it is the rubric's job.
function MonitorComms({ reports, serverTime }: { reports: MonitorReport[]; serverTime: string }) {
  if (reports.length === 0) return <>—</>;
  const last = reports.reduce((a, b) => new Date(b.delivered_at) > new Date(a.delivered_at) ? b : a);
  const missed = reports.filter((report) => report.missed).length;
  const kind = last.delivery === "phone_incoming" ? "звонок" : "доклад";
  const start = last.answered_at ?? last.delivered_at;
  let reaction: string;
  let alarm = false;
  if (last.missed) {
    reaction = "звонок пропущен";
    alarm = true;
  } else if (last.delivery === "phone_incoming" && !last.answered_at) {
    reaction = "звонит";
  } else if (last.reaction_at) {
    reaction = `реакция через ${seconds(start, last.reaction_at)} с`;
  } else {
    reaction = `нет реакции ${seconds(start, serverTime)} с`;
  }
  return (
    <div className="monitor-comms">
      <div>{last.from_label}: {kind} {seconds(last.delivered_at, serverTime)} с назад</div>
      <div className={alarm ? "monitor-comms-alarm" : undefined}>{reaction}</div>
      {missed > 0 && <div className="monitor-comms-alarm">пропущено звонков: {missed}</div>}
    </div>
  );
}
