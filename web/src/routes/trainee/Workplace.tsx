import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useState, type FormEvent } from "react";
import { useOutletContext } from "react-router-dom";
import { executeCommand, type Command, type Receipt } from "../../api/commands";
import { errorMessage } from "../../api/errors";
import type { Me } from "../../api/useMe";
import { itemQueryKey, myItemsQueryKey, myRunQueryKey, useItem, useMyItems, useMyRun } from "../../api/workplace";
import { availableLocalStorage, clearPending, loadPending, savePending, type PendingCommand } from "../../commands/pending";
import { IncidentCard } from "../../components/IncidentCard";
import { formatDateTime } from "../../format";
import { reactionLabel } from "../../labels";

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
  const run = useMyRun();
  const workstationMatches = !run.data || run.data.workstation_no === me.workstation?.number;
  const items = useMyItems(!!run.data && workstationMatches);
  const [selectedItemId, setSelectedItemId] = useState("");
  const [justCompleted, setJustCompleted] = useState(false);
  const itemId = selectedItemId || run.data?.current_item_id || items.data?.find((candidate) => candidate.state !== "closed")?.id || items.data?.[0]?.id || "";
  const item = useItem(itemId, workstationMatches);

  if (run.isPending) return <p>Ожидание назначения…</p>;
  if (run.isError) return <p className="error">{errorMessage(run.error)}</p>;
  if (!run.data) return justCompleted ? <Completed /> : <Waiting me={me} />;

  return (
    <section>
      <h1>{run.data.lesson.title}</h1>
      <p>Рабочее место занятия: РМ-{run.data.workstation_no}. В очереди: {run.data.queue_left}.</p>
      {!workstationMatches && (
        <p role="alert" className="error">Занятие назначено на РМ-{run.data.workstation_no}. Войдите на этом рабочем месте.</p>
      )}
      {workstationMatches && items.isError && <p className="error">{errorMessage(items.error)}</p>}
      {workstationMatches && items.data && items.data.length > 1 && (
        <nav className="item-list" aria-label="Карточки">
          {items.data.map((candidate) => (
            <button type="button" key={candidate.id} className={candidate.id === itemId ? "active" : undefined} onClick={() => setSelectedItemId(candidate.id)}>
              № {candidate.card_number} · {reactionLabel(candidate.reaction)}
            </button>
          ))}
        </nav>
      )}
      {workstationMatches && item.isPending && <p>Загрузка карточки…</p>}
      {workstationMatches && item.isError && <p className="error">{errorMessage(item.error)}</p>}
      {workstationMatches && item.data && <ItemWorkplace key={item.data.id} me={me} item={item.data} onClosed={() => setJustCompleted(true)} />}
    </section>
  );
}

function Completed() {
  return (
    <section>
      <h1>Упражнение завершено</h1>
      <p className="notice">Карточка закрыта. Балл появится после оценки в следующих срезах.</p>
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

function ItemWorkplace({ me, item, onClosed }: { me: Me; item: NonNullable<ReturnType<typeof useItem>["data"]>; onClosed: () => void }) {
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
      if (nextReceipt.outcome === "applied" && nextReceipt.item_state === "closed") onClosed();
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
  const editable = item.state !== "closed" && (item.reaction === "received" || item.reaction === "not_accepted");
  const closable = item.state !== "closed" && ["accepted", "not_accepted", "completed", "completed_without_team"].includes(item.reaction);

  return (
    <section>
      <IncidentCard card={item.card} />
      <dl>
        <dt>Состояние</dt><dd>{reactionLabel(item.reaction)}</dd>
        <dt>Выдана</dt><dd>{formatDateTime(item.offered_at)}</dd>
        <dt>Открыть</dt><dd>{remaining(item.deadlines.open_at, clockAnchor.server + clientNow - clockAnchor.client)}</dd>
        <dt>Первичное решение</dt><dd>{remaining(item.deadlines.primary_at, clockAnchor.server + clientNow - clockAnchor.client)}</dd>
        {item.deadlines.complete_at && <><dt>Завершить</dt><dd>{remaining(item.deadlines.complete_at, clockAnchor.server + clientNow - clockAnchor.client)}</dd></>}
      </dl>
      {item.state === "closed" ? (
        <p className="notice">Упражнение завершено.</p>
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
      {item.state !== "offered" && item.state !== "closed" && (
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

function remaining(deadline: string | undefined, serverNowMs: number): string {
  if (!deadline) return "—";
  const seconds = Math.ceil((new Date(deadline).getTime() - serverNowMs) / 1_000);
  if (seconds <= 0) return `срок истёк (${formatDateTime(deadline)})`;
  const minutes = Math.floor(seconds / 60);
  const rest = seconds % 60;
  return `${minutes}:${String(rest).padStart(2, "0")} (до ${formatDateTime(deadline)})`;
}
