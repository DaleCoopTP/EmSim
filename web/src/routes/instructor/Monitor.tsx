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
    <section>
      <p><Link to={`/instructor/lessons/${lessonId}`}>← К занятию</Link></p>
      <p><Link to={`/instructor/lessons/${lessonId}/assessments`}>Перейти к разбору карточек →</Link></p>
      <h1>Монитор: {data.lesson.title}</h1>
      <p>Сервер: {formatDateTime(data.server_time)}{data.lesson.state === "stopped" && " · занятие остановлено, карточки закрываются фоново"}</p>
      {data.rows.length === 0 ? (
        <p>Пока никто не начал.</p>
      ) : (
        <table>
          <thead>
            <tr><th>РМ</th><th>Обучаемый</th><th>Онлайн</th><th>Открытые карточки</th><th>Осталось</th><th>Готово</th><th>Последнее действие</th></tr>
          </thead>
          <tbody>{data.rows.map((row) => <MonitorRowView key={row.run_id} row={row} />)}</tbody>
        </table>
      )}
    </section>
  );
}

function MonitorRowView({ row }: { row: MonitorRow }) {
  return (
    <tr>
      <td>№ {row.workstation_no}</td>
      <td>{row.user.full_name}</td>
      <td>{row.online ? "да" : "нет"}</td>
      <td>
        {row.active_items.length === 0 && "—"}
        {row.active_items.map((item) => (
          <div key={item.id}>
            № {item.card_number} · {reactionLabel(item.reaction)}
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
