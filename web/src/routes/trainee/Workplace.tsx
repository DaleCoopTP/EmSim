import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useState, type FormEvent } from "react";
import { useOutletContext } from "react-router-dom";
import { executeCommand, type Command, type Receipt } from "../../api/commands";
import { errorMessage } from "../../api/errors";
import { useEventStream } from "../../api/realtime";
import type { Me } from "../../api/useMe";
import { itemQueryKey, myItemsQueryKey, myRunQueryKey, useItem, useMyItems, useMyRun } from "../../api/workplace";
import { availableLocalStorage, clearPending, loadPending, savePending, type PendingCommand } from "../../commands/pending";
import { IncidentCard } from "../../components/IncidentCard";
import { formatDateTime } from "../../format";
import { reactionLabel } from "../../labels";

const deliveryLabels: Record<string, string> = {
  notice: "Сообщение",
  phone_incoming: "Входящий звонок",
  spawn_card: "Новая карточка",
};

const rejectionLabels: Record<string, string> = {
  stale_seq: "Карточка уже изменилась. Данные обновлены — повторите действие, если оно всё ещё нужно.",
  item_closed: "Карточка уже закрыта.",
  lesson_stopped: "Занятие остановлено преподавателем.",
  transition_not_allowed: "Это действие сейчас недоступно.",
  comment_required: "Для отказа требуется комментарий.",
  invalid_payload: "Проверьте заполненные данные.",
};

export function WorkplaceRoute() {
  const me = useOutletContext<Me>();
  const queryClient = useQueryClient();
  const run = useMyRun();
  const workstationMatches = !run.data || run.data.workstation_no === me.workstation?.number;
  const items = useMyItems(!!run.data && workstationMatches);
  const [selectedItemId, setSelectedItemId] = useState("");
  // The run disappears from /my/run the instant its last item closes
  // (ActiveRunByUser only ever returns an active run) — but the trainee
  // must still be able to see that just-closed card and send a
  // control_report against it (slice-4-plan.md's C10: "после close
  // оставить только что закрытую карточку в UI"). lastItemId keeps
  // fetching the same item after the run itself is gone, for the rest of
  // this mount only — a reload starts over with nothing to show, which
  // is explicitly out of scope until a later slice's history screen.
  const [lastItemId, setLastItemId] = useState("");
  const itemId = selectedItemId || run.data?.current_item_id || items.data?.find((candidate) => candidate.state !== "closed")?.id || items.data?.[0]?.id || lastItemId || "";
  const item = useItem(itemId, (workstationMatches && !!run.data) || (!run.data && itemId === lastItemId));

  // Remember the most recent real item id across a render, without an
  // effect: React's documented pattern for deriving state from a
  // previous render — this call is a no-op once itemId === lastItemId.
  if (itemId && itemId !== lastItemId) {
    setLastItemId(itemId);
  }

  // RFC-001 §7.7: stream-first. A fresh stream.ready, or any resync,
  // means "read a snapshot now" — the same three reads this route
  // already keeps current (my/run, the full my/items, the open item).
  useEventStream("/my/stream", () => {
    void queryClient.invalidateQueries({ queryKey: myRunQueryKey });
    void queryClient.invalidateQueries({ queryKey: myItemsQueryKey });
    if (itemId) void queryClient.invalidateQueries({ queryKey: itemQueryKey(itemId) });
  });

  if (run.isPending) return <p>Ожидание назначения…</p>;
  if (run.isError) return <p className="error">{errorMessage(run.error)}</p>;

  if (!run.data) {
    if (!lastItemId) return <Waiting me={me} />;
    return (
      <section>
        {item.isPending && <p>Загрузка карточки…</p>}
        {item.isError && <p className="error">{errorMessage(item.error)}</p>}
        {item.data && <ItemWorkplace key={item.data.id} me={me} item={item.data} />}
      </section>
    );
  }

  return (
    <section>
      <h1>{run.data.lesson.title}</h1>
      <p>Рабочее место занятия: РМ-{run.data.workstation_no}. В очереди: {run.data.queue_left}.</p>
      {run.data.lesson.state === "stopped" && (
        <p role="alert" className="notice">Занятие остановлено преподавателем{run.data.lesson.stop_reason ? `: ${run.data.lesson.stop_reason}` : ""}. Открытые карточки прерываются фоново.</p>
      )}
      {!workstationMatches && (
        <p role="alert" className="error">Занятие назначено на РМ-{run.data.workstation_no}. Войдите на этом рабочем месте.</p>
      )}
      {workstationMatches && items.isError && <p className="error">{errorMessage(items.error)}</p>}
      {workstationMatches && items.data && items.data.length > 1 && (
        <nav className="item-list" aria-label="Карточки">
          {items.data.map((candidate) => (
            <button type="button" key={candidate.id} className={candidate.id === itemId ? "active" : undefined} onClick={() => setSelectedItemId(candidate.id)}>
              № {candidate.card_number} · {reactionLabel(candidate.reaction)}
              {candidate.interruptions.length > 0 && " ⚠"}
            </button>
          ))}
        </nav>
      )}
      {workstationMatches && item.isPending && <p>Загрузка карточки…</p>}
      {workstationMatches && item.isError && <p className="error">{errorMessage(item.error)}</p>}
      {workstationMatches && item.data && <ItemWorkplace key={item.data.id} me={me} item={item.data} />}
    </section>
  );
}

function Waiting({ me }: { me: Me }) {
  return (
    <section>
      <h1>{me.user.full_name}</h1>
      <p>{me.workstation ? `Рабочее место: ${me.workstation.label} (№ ${me.workstation.number})` : "Рабочее место не выбрано."}</p>
      <p className="notice">Ожидайте назначения занятия.</p>
    </section>
  );
}

function ItemWorkplace({ me, item }: { me: Me; item: NonNullable<ReturnType<typeof useItem>["data"]> }) {
  const queryClient = useQueryClient();
  const [storage] = useState(() => availableLocalStorage());
  const [pending, setPending] = useState<PendingCommand | null>(() => storage ? loadPending(storage, me.user.id, item.id) : null);
  const [receipt, setReceipt] = useState<Receipt | null>(null);
  const [commandError, setCommandError] = useState<unknown>(null);
  const [comment, setComment] = useState("");
  const [okrug, setOkrug] = useState(item.card.address.okrug ?? "");
  const [clockAnchor] = useState(() => ({ client: Date.now(), server: new Date(item.server_time).getTime() }));
  const [clientNow, setClientNow] = useState(() => Date.now());

  useEffect(() => {
    const timer = window.setInterval(() => setClientNow(Date.now()), 1_000);
    return () => window.clearInterval(timer);
  }, []);

  const refresh = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: itemQueryKey(item.id) }),
      queryClient.invalidateQueries({ queryKey: myItemsQueryKey }),
      queryClient.invalidateQueries({ queryKey: myRunQueryKey }),
    ]);
  };

  const deliver = async (value: PendingCommand) => {
    setCommandError(null);
    try {
      const nextReceipt = await executeCommand(value.item_id, value.command);
      if (storage) clearPending(storage, me.user.id, value.item_id, value.command.command_id);
      setPending(null);
      setReceipt(nextReceipt);
      await refresh();
    } catch (error) {
      setCommandError(error);
    }
  };

  useEffect(() => {
    if (pending) void deliver(pending);
    // The stored body must be replayed exactly once when this item screen
    // mounts. Further retries are explicit (button below) or a remount.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const send = (type: Command["type"], payload: Record<string, unknown>) => {
    if (pending) return;
    const command: Command = {
      command_id: crypto.randomUUID(),
      expected_seq: item.seq,
      type,
      payload,
      client_at: new Date().toISOString(),
    };
    const value = { item_id: item.id, command };
    try {
      if (storage) savePending(storage, me.user.id, item.id, command);
      setPending(value);
      void deliver(value);
    } catch (error) {
      setCommandError(error);
    }
  };

  const addComment = (event: FormEvent) => {
    event.preventDefault();
    if (comment.trim()) send("add_comment", { text: comment.trim() });
  };
  const rejected = receipt?.outcome === "rejected" ? rejectionLabels[receipt.error_code ?? ""] ?? receipt.error_code : undefined;
  const open = item.state === "offered";
  const editable = item.state !== "closed" && item.state !== "interrupted" && (item.reaction === "received" || item.reaction === "not_accepted");
  const closable = item.state !== "closed" && item.state !== "interrupted" && ["accepted", "not_accepted", "completed", "completed_without_team"].includes(item.reaction);
  const finished = item.state === "closed" || item.state === "interrupted";

  return (
    <section>
      {item.interruptions.length > 0 && (
        <p role="alert" className="notice">
          Карточка была прервана перезапуском сервера ({item.interruptions.length}×, последний раз {formatDateTime(item.interruptions[item.interruptions.length - 1].detected_at)}). Норматив времени по ней не учитывается.
        </p>
      )}
      <IncidentCard card={item.card} />
      <dl>
        <dt>Состояние</dt><dd>{reactionLabel(item.reaction)}</dd>
        <dt>Выдана</dt><dd>{formatDateTime(item.offered_at)}</dd>
        <dt>Открыть</dt><dd>{remaining(item.deadlines.open_at, clockAnchor.server + clientNow - clockAnchor.client)}</dd>
        <dt>Первичное решение</dt><dd>{remaining(item.deadlines.primary_at, clockAnchor.server + clientNow - clockAnchor.client)}</dd>
        {item.deadlines.complete_at && <><dt>Завершить</dt><dd>{remaining(item.deadlines.complete_at, clockAnchor.server + clientNow - clockAnchor.client)}</dd></>}
      </dl>
      {item.events.length > 0 && (
        <div className="item-events">
          <h3>Сообщения</h3>
          <ul>
            {item.events.map((event) => (
              <li key={event.key}>
                <strong>{deliveryLabels[event.delivery] ?? event.delivery}</strong>{event.from ? ` от ${event.from}` : ""}: {event.text}
                {" "}({formatDateTime(event.delivered_at)}{event.late ? ", с опозданием" : ""})
              </li>
            ))}
          </ul>
        </div>
      )}
      {finished ? (
        <>
          <p className="notice">{item.state === "interrupted" ? "Карточка прервана окончанием занятия." : "Упражнение завершено."}</p>
          <ControlReportForm pending={!!pending} onSend={(text) => send("control_report", { text })} />
        </>
      ) : (
        <div className="item-actions">
          {open && <button type="button" disabled={!!pending} onClick={() => send("open", {})}>Открыть карточку</button>}
          {!open && item.allowed_transitions.includes("accepted") && <button type="button" disabled={!!pending} onClick={() => send("set_status", { status: "accepted" })}>Принять</button>}
          {!open && item.allowed_transitions.includes("not_accepted") && (
            <button type="button" disabled={!!pending || comment.trim() === ""} onClick={() => send("set_status", { status: "not_accepted", comment: comment.trim() })}>Не принять</button>
          )}
          {closable && <button type="button" disabled={!!pending} onClick={() => send("close", {})}>Завершить упражнение</button>}
        </div>
      )}

      {editable && (
        <form className="inline-form" onSubmit={(event) => { event.preventDefault(); if (okrug.trim()) send("set_card_field", { path: "/card/address/okrug", value: okrug.trim() }); }}>
          <label>Округ<input value={okrug} onChange={(event) => setOkrug(event.target.value)} /></label>
          <button type="submit" disabled={!!pending || okrug.trim() === "" || okrug.trim() === item.card.address.okrug}>Сохранить округ</button>
        </form>
      )}
      {item.state !== "offered" && !finished && (
        <form className="inline-form" onSubmit={addComment}>
          <label>Комментарий<textarea value={comment} onChange={(event) => setComment(event.target.value)} /></label>
          <button type="submit" disabled={!!pending || comment.trim() === ""}>Добавить комментарий</button>
        </form>
      )}
      {!storage && <p role="alert" className="error">Локальное хранилище недоступно: автоматическое восстановление команды после сбоя не гарантируется.</p>}
      {pending && <p className="notice">Команда отправляется…</p>}
      {pending && commandError && <button type="button" onClick={() => void deliver(pending)}>Повторить отправку</button>}
      {commandError && <p role="alert" className="error">{errorMessage(commandError)}</p>}
      {rejected && <p role="alert" className="error">{rejected}</p>}
      {receipt?.outcome === "applied" && <p>Действие сохранено{receipt.replayed ? " (восстановлено)" : ""}.</p>}
    </section>
  );
}

// ControlReportForm is RFC-001 §7.5's post-close message: a plain-text
// note the trainee sends about their own already-closed card, allowed
// even after the lesson itself has stopped. It never changes the card's
// reaction/closed_at or evidence — it is journalled alongside the
// command log for the instructor to see during review.
function ControlReportForm({ pending, onSend }: { pending: boolean; onSend: (text: string) => void }) {
  const [text, setText] = useState("");
  return (
    <form
      className="inline-form"
      onSubmit={(event) => {
        event.preventDefault();
        if (!text.trim()) return;
        onSend(text.trim());
      }}
    >
      <label>Сообщение в отдел контроля (после закрытия)<textarea value={text} onChange={(event) => setText(event.target.value)} /></label>
      <button type="submit" disabled={pending || text.trim() === ""}>Отправить</button>
    </form>
  );
}

function remaining(deadline: string | undefined, serverNowMs: number): string {
  if (!deadline) return "—";
  const seconds = Math.ceil((new Date(deadline).getTime() - serverNowMs) / 1_000);
  if (seconds <= 0) return `срок истёк (${formatDateTime(deadline)})`;
  const minutes = Math.floor(seconds / 60);
  const rest = seconds % 60;
  return `${minutes}:${String(rest).padStart(2, "0")} (до ${formatDateTime(deadline)})`;
}
