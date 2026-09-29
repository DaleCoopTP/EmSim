import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { useOutletContext } from "react-router-dom";
import { executeCommand, type Command, type Receipt } from "../../api/commands";
import { errorMessage } from "../../api/errors";
import { useEventStream } from "../../api/realtime";
import type { Me } from "../../api/useMe";
import { itemQueryKey, myItemsQueryKey, myRunQueryKey, useItem, useMyItems, useMyRun, type CardView, type Item, type ItemSummary, type MyRun } from "../../api/workplace";
import { availableLocalStorage, clearPending, loadPending, savePending, type PendingCommand } from "../../commands/pending";
import { IncidentCard } from "../../components/IncidentCard";
import { formatDateTime } from "../../format";
import { cardStatusAlarm, cardStatusLabel, reactionLabel } from "../../labels";
import { Arm112Main } from "./Arm112Main";
import { DDSArmCard } from "./DDSArmCard";
import { Operator112Workplace, type IntakeItem } from "./Operator112Workplace";

type DDSItem = Omit<Item, "card"> & { card: CardView };

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
  comment_required: "Для этого статуса требуется комментарий.",
  call_required: "Сначала завершите обязательный звонок.",
  call_in_progress: "Сначала завершите текущий звонок.",
  invalid_payload: "Проверьте заполненные данные.",
  call_missed: "Звонок уже пропущен: абонент положил трубку.",
  call_not_active: "Этот звонок уже завершён.",
};

// ADR-031: contact roles group the DDS phone's contacts.
const contactRoleLabels: Record<string, string> = {
  crew: "Бригада",
  control_112: "Отдел контроля 112",
  applicant: "Заявитель",
  other: "Прочие",
};
const contactRoleOrder = ["crew", "control_112", "applicant", "other"];

export function WorkplaceRoute() {
  const me = useOutletContext<Me>();
  const queryClient = useQueryClient();
  const run = useMyRun();
  const workstationMatches = !run.data || run.data.workstation_no === me.workstation?.number;
  const items = useMyItems(!!run.data && workstationMatches);
  const [selectedItemId, setSelectedItemId] = useState("");
  const [queueSearch, setQueueSearch] = useState("");
  // The 112 main screen's "Принять" opens the ringing card and answers it
  // at once (Operator112ProfileCase's acceptOnOpen).
  const [acceptItemId, setAcceptItemId] = useState("");
  const openItem = (id: string, accept?: boolean) => { setAcceptItemId(accept ? id : ""); setSelectedItemId(id); };
  // The run disappears from /my/run the instant its last item closes
  // (ActiveRunByUser only ever returns an active run) — but the trainee
  // must still be able to see that just-closed card and send a
  // control_report against it (slice-4-plan.md's C10: "после close
  // оставить только что закрытую карточку в UI"). lastItemId keeps
  // fetching the same item after the run itself is gone, for the rest of
  // this mount only — a reload starts over with nothing to show, which
  // is explicitly out of scope until a later slice's history screen.
  const [lastItemId, setLastItemId] = useState("");
  // Keep the completed 112 queue visible for this mount after the last
  // case finishes and /my/run correctly stops returning an active run.
  const [completedQueue, setCompletedQueue] = useState<{ run: MyRun; items: ItemSummary[] } | null>(null);
  // An assigned trainee lands on the queue, not directly inside a card.
  // selectedItemId is therefore deliberately empty until they open a row.
  // lastItemId only supports the post-close control report described below.
  const itemId = selectedItemId || lastItemId;
  const item = useItem(itemId, (workstationMatches && !!run.data && !!selectedItemId) || (!run.data && !!itemId && completedQueue?.items.some((candidate) => candidate.id === itemId) === true));

  if (run.data?.exercise_type === "operator112_intake" && items.data?.length &&
      (completedQueue?.run.run_id !== run.data.run_id || completedQueue.items !== items.data)) {
    setCompletedQueue({ run: run.data, items: items.data });
  }

  // Remember the most recent real item id across a render, without an
  // effect: React's documented pattern for deriving state from a
  // previous render — this call is a no-op once itemId === lastItemId.
  if (selectedItemId && selectedItemId !== lastItemId) {
    setLastItemId(selectedItemId);
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
    if (completedQueue) {
      const { run: previousRun, items: previousItems } = completedQueue;
      const queueItems = previousItems.map((candidate) => candidate.id === item.data?.id ? { ...candidate, state: item.data.state } : candidate);
      if (!selectedItemId) return <Arm112Main me={me} run={previousRun} items={queueItems} search={queueSearch} onSearch={setQueueSearch} onOpen={openItem} />;
      return <section className="trainee-workplace">
        <header className="workplace-header"><div><p className="workplace-kicker">Рабочее место 112 · РМ-{previousRun.workstation_no}</p>
          <h1>Обработанный кейс</h1></div></header>
        {selectedItemId && <>
          {!isCardOnly(item.data) && <button type="button" className="back-to-queue intake-back-to-queue" onClick={() => setSelectedItemId("")}>← К списку вызовов</button>}
          {item.isPending && <p>Загрузка карточки…</p>}
          {item.isError && <p className="error">{errorMessage(item.error)}</p>}
          {item.data && <Operator112Workplace key={item.data.id} me={me} item={item.data as unknown as IntakeItem} onClose={() => setSelectedItemId("")} />}
        </>}
      </section>;
    }
    if (!lastItemId) return <Waiting me={me} />;
    return (
      <section>
        {item.isPending && <p>Загрузка карточки…</p>}
        {item.isError && <p className="error">{errorMessage(item.error)}</p>}
		{item.data && (item.data.exercise_type === "operator112_intake" ? <Operator112Workplace key={item.data.id} me={me} item={item.data as unknown as IntakeItem} /> : <ItemWorkplace key={item.data.id} me={me} item={item.data as DDSItem} />)}
      </section>
    );
  }

  const is112 = run.data.exercise_type === "operator112_intake";
  const stoppedNotice = run.data.lesson.state === "stopped" &&
    <p role="alert" className="notice">Занятие остановлено преподавателем{run.data.lesson.stop_reason ? `: ${run.data.lesson.stop_reason}` : ""}. Открытые карточки прерываются фоново.</p>;
  const workstationNotice = !workstationMatches &&
    <p role="alert" className="error">Занятие назначено на РМ-{run.data.workstation_no}. Войдите на этом рабочем месте.</p>;
  if (!selectedItemId) {
    return <Arm112Main me={me} run={run.data} items={workstationMatches ? items.data ?? [] : []} search={queueSearch} onSearch={setQueueSearch} onOpen={openItem}
      statusOf={is112 ? undefined : (candidate) => `${cardStatusLabel(candidate.card_status)}${candidate.interruptions.length > 0 ? " ⚠" : ""}`}
      notices={<>{stoppedNotice}{workstationNotice}{workstationMatches && items.isError && <p className="error">{errorMessage(items.error)}</p>}</>} />;
  }
  // A DDS card of a service with terminal statuses (ADR-030) is drawn as
  // the ARM-112 card with its own × back to the list; the old pilot
  // services keep the plain layout and its header.
  const ddsArm = !is112 && !!item.data && item.data.exercise_type !== "operator112_intake" && (item.data.terminal_statuses ?? []).length > 0;

  return (
    <section className="trainee-workplace">
      {!(selectedItemId && (is112 || ddsArm)) && <header className="workplace-header">
        <div>
		  <p className="workplace-kicker">Рабочее место {run.data.exercise_type === "operator112_intake" ? "112" : "ДДС"} · РМ-{run.data.workstation_no}</p>
		  <h1>{selectedItemId ? run.data.exercise_type === "operator112_intake" ? "Входящий вызов" : "Карточка происшествия" : run.data.lesson.title}</h1>
        </div>
        <dl className="workplace-facts">
          <dt>В очереди</dt><dd>{run.data.queue_left}</dd>
          <dt>Режим</dt><dd>{run.data.mode === "intro" ? "ознакомительный" : "тренировка"}</dd>
        </dl>
      </header>}
      {stoppedNotice}
      {workstationNotice}
      {workstationMatches && items.isError && <p className="error">{errorMessage(items.error)}</p>}
      {workstationMatches && selectedItemId && (
        <>
		  {!isCardOnly(item.data) && !ddsArm && <button type="button" className={`back-to-queue${run.data.exercise_type === "operator112_intake" ? " intake-back-to-queue" : ""}`} onClick={() => setSelectedItemId("")}>← К списку {run.data.exercise_type === "operator112_intake" ? "вызовов" : "происшествий"}</button>}
          {item.isPending && <p>Загрузка карточки…</p>}
          {item.isError && <p className="error">{errorMessage(item.error)}</p>}
		  {item.data && (item.data.exercise_type === "operator112_intake" ? <Operator112Workplace key={item.data.id} me={me} item={item.data as unknown as IntakeItem} acceptOnOpen={acceptItemId === item.data.id} onClose={() => setSelectedItemId("")} /> : <ItemWorkplace key={item.data.id} me={me} item={item.data as DDSItem} onClose={() => setSelectedItemId("")} />)}
        </>
      )}
    </section>
  );
}

// A card_only/full_case 112 item closes through the × of its own bottom bar, as in ARM-112.
function isCardOnly(item: unknown): boolean {
  const mode = (item as Partial<IntakeItem> | undefined)?.intake_state?.mode;
  return mode === "card_only" || mode === "full_case";
}

function Waiting({ me }: { me: Me }) {
  return (
    <section className="waiting-workplace">
      <div className="waiting-icon" aria-hidden="true">⌁</div>
      <div>
        <p className="workplace-kicker">Рабочее место ДДС</p>
        <h1>{me.user.full_name}</h1>
        <p>{me.workstation ? `${me.workstation.label} · РМ-${me.workstation.number}` : "Рабочее место не выбрано."}</p>
        <p className="notice">Ожидайте назначения занятия.</p>
      </div>
    </section>
  );
}

function ItemWorkplace({ me, item, onClose }: { me: Me; item: DDSItem; onClose?: () => void }) {
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
  const finished = item.state === "closed" || item.state === "interrupted";
  // ADR-030: a service whose workflow names terminal statuses is worked
  // through the status pencil only — the terminal status closes the card.
  // An empty list is a slice 2–7 pilot service with the old controls.
  const terminalStatuses = item.terminal_statuses ?? [];
  const legacy = terminalStatuses.length === 0;
  const editable = legacy && !finished && (item.reaction === "received" || item.reaction === "not_accepted");
  const closable = legacy && !finished && ["accepted", "not_accepted", "completed", "completed_without_team"].includes(item.reaction);

  const serverNowMs = clockAnchor.server + clientNow - clockAnchor.client;
  const interruptedNotice = item.interruptions.length > 0 && (
    <p role="alert" className="notice">
      Карточка была прервана перезапуском сервера ({item.interruptions.length}×, последний раз {formatDateTime(item.interruptions[item.interruptions.length - 1].detected_at)}). Норматив времени по ней не учитывается.
    </p>
  );
  const feedback = <>
    {!storage && <p role="alert" className="error">Локальное хранилище недоступно: автоматическое восстановление команды после сбоя не гарантируется.</p>}
    {pending && <p className="notice">Команда отправляется…</p>}
    {pending && commandError && <button type="button" onClick={() => void deliver(pending)}>Повторить отправку</button>}
    {commandError && <p role="alert" className="error">{errorMessage(commandError)}</p>}
    {rejected && <p role="alert" className="error">{rejected}</p>}
    {receipt?.outcome === "applied" && <p>Действие сохранено{receipt.replayed ? " (восстановлено)" : ""}.</p>}
  </>;

  if (!legacy) {
    return (
      <section className="dds-workplace dds-arm-workplace">
        {interruptedNotice}
        <div className={`dds-arm-layout${open ? " is-offered" : ""}`}>
          <DDSArmCard item={item} serverNowMs={serverNowMs} opening={!!pending} onOpen={() => send("open", {})} onClose={() => onClose?.()}
            statusSlot={<ServiceStatusBlock compact item={item} terminalStatuses={terminalStatuses} disabled={!!pending || finished} onSave={(status, text) => send("set_status", text ? { status, comment: text } : { status })} />}
            comms={<CrewCommsPanel
            item={item}
            serverNowMs={serverNowMs}
            disabled={!!pending || finished}
            onAnswer={(eventKey) => send("answer_incoming", { event_key: eventKey })}
            onEndIncoming={(callId) => send("call_end", { call_id: callId, accepted_by: "", summary: "", recording: null })}
            phone={finished ? undefined : <PhonePanel item={item} onChanged={refresh} />} />}
            footer={<div className="arm112-feedback dds-arm-feedback" aria-live="polite">
              {finished && <p className="notice">{item.state === "interrupted" ? "Карточка прервана окончанием занятия." : "Упражнение завершено."}</p>}
              {feedback}
            </div>} />
        </div>
        {finished && <ControlReportForm pending={!!pending} onSend={(text) => send("control_report", { text })} />}
      </section>
    );
  }

  return (
    <section className="dds-workplace">
      {item.interruptions.length > 0 && (
        <p role="alert" className="notice">
          Карточка была прервана перезапуском сервера ({item.interruptions.length}×, последний раз {formatDateTime(item.interruptions[item.interruptions.length - 1].detected_at)}). Норматив времени по ней не учитывается.
        </p>
      )}
      <section className="dds-item-status" aria-label="Статус обработки карточки">
        <div><span>Статус службы</span><strong>{reactionLabel(item.reaction)}</strong></div>
        {!legacy && <div><span>Статус карточки</span><strong><CardStatusBadge status={item.card_status} /></strong></div>}
        <div><span>Выдана</span><strong>{formatDateTime(item.offered_at)}</strong></div>
        <div><span>Открыть</span><strong>{remaining(item.deadlines.open_at, clockAnchor.server + clientNow - clockAnchor.client)}</strong></div>
        <div><span>Первичное решение</span><strong>{remaining(item.deadlines.primary_at, clockAnchor.server + clientNow - clockAnchor.client)}</strong></div>
        {item.deadlines.complete_at && <div><span>Завершить</span><strong>{remaining(item.deadlines.complete_at, clockAnchor.server + clientNow - clockAnchor.client)}</strong></div>}
      </section>
      {legacy || open ? (
        <IncidentCard card={item.card} />
      ) : (
        <div className="dds-work-area">
          <IncidentCard card={item.card} mineSlot={
            <ServiceStatusBlock item={item} terminalStatuses={terminalStatuses} disabled={!!pending || finished} onSave={(status, text) => send("set_status", text ? { status, comment: text } : { status })} />
          } />
          <CrewCommsPanel
            item={item}
            serverNowMs={clockAnchor.server + clientNow - clockAnchor.client}
            disabled={!!pending || finished}
            onAnswer={(eventKey) => send("answer_incoming", { event_key: eventKey })}
            onEndIncoming={(callId) => send("call_end", { call_id: callId, accepted_by: "", summary: "", recording: null })}
            phone={finished ? undefined : <PhonePanel item={item} onChanged={refresh} />}
          />
        </div>
      )}
      {legacy && item.events.length > 0 && (
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
          {legacy && !open && item.allowed_transitions.includes("accepted") && <button type="button" disabled={!!pending} onClick={() => send("set_status", { status: "accepted" })}>Принять</button>}
          {legacy && !open && item.allowed_transitions.includes("not_accepted") && (
            <button type="button" disabled={!!pending || comment.trim() === ""} onClick={() => send("set_status", { status: "not_accepted", comment: comment.trim() })}>Не принять</button>
          )}
          {legacy && !open && <PhonePanel item={item} onChanged={refresh} />}
          {closable && <button type="button" disabled={!!pending} onClick={() => send("close", {})}>Завершить упражнение</button>}
        </div>
      )}

      {editable && (
        <form className="inline-form" onSubmit={(event) => { event.preventDefault(); if (okrug.trim()) send("set_card_field", { path: "/card/address/okrug", value: okrug.trim() }); }}>
          <label>Округ<input value={okrug} onChange={(event) => setOkrug(event.target.value)} /></label>
          <button type="submit" disabled={!!pending || okrug.trim() === "" || okrug.trim() === item.card.address.okrug}>Сохранить округ</button>
        </form>
      )}
      {legacy && item.state !== "offered" && !finished && (
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

// PhonePanel is intentionally a small browser simulator, not a SIP client.
// A not-yet-uploaded recording stays in this component's memory only; a tab
// reload therefore has the explicitly documented "missing recording" outcome.
function PhonePanel({ item, onChanged }: { item: DDSItem; onChanged: () => Promise<void> }) {
  const contacts = item.card.contacts ?? [];
  // ДДС-3/ADR-032: an outgoing call's «Кто принял»/«Суть сообщения» is
  // only still required on a legacy (pre-ADR-030 pilot) service — the
  // same terminal_statuses check ServiceStatusBlock's own "legacy" uses.
  const legacy = (item.terminal_statuses ?? []).length === 0;
  // ADR-031: an answered incoming call occupies the line.
  const incomingActive = item.calls.some((call) => call.direction === "incoming" && !call.ended_at);
  const phraseText = (key: string, phrase: "greeting" | "ack") => contacts.find((candidate) => candidate.key === key)?.phrases?.[phrase];
  const grouped = contactRoleOrder
    .map((role) => ({ role, contacts: contacts.filter((candidate) => (candidate.role ?? "other") === role) }))
    .filter((group) => group.contacts.length > 0);
  const [contact, setContact] = useState(contacts[0]?.key ?? "");
  const [callId, setCallId] = useState<string | null>(null);
  const [callEndSeq, setCallEndSeq] = useState<number | null>(null);
  const [acceptedBy, setAcceptedBy] = useState("");
  const [summary, setSummary] = useState("");
  const [muted, setMuted] = useState(false);
  const [isRecording, setIsRecording] = useState(false);
  const [pendingUpload, setPendingUpload] = useState<{ callId: string; blob: Blob } | null>(null);
  const [isEnding, setIsEnding] = useState(false);
  const [status, setStatus] = useState("");
  const [error, setError] = useState("");
  const streamRef = useRef<MediaStream | null>(null);
  const recorderRef = useRef<MediaRecorder | null>(null);
  const recordingRef = useRef<Blob | null>(null);
  const recordingDoneRef = useRef<Promise<Blob> | null>(null);
  const callEndCommandRef = useRef<Command | null>(null);

  const releaseMicrophone = () => {
    streamRef.current?.getTracks().forEach((track) => track.stop());
    streamRef.current = null;
  };

  useEffect(() => () => {
    if (recorderRef.current?.state === "recording") recorderRef.current.stop();
    releaseMicrophone();
  }, []);

  const stopRecording = async (): Promise<Blob | null> => {
    const recorder = recorderRef.current;
    if (!recorder) return recordingRef.current;
    if (recorder.state !== "inactive") recorder.stop();
    const blob = await recordingDoneRef.current;
    recorderRef.current = null;
    recordingDoneRef.current = null;
    setIsRecording(false);
    return blob;
  };

  const uploadRecording = async (upload: { callId: string; blob: Blob }) => {
    setError("");
    const form = new FormData();
    form.append("file", upload.blob, "report.webm");
    try {
      const response = await fetch(`/api/v1/items/${encodeURIComponent(item.id)}/calls/${encodeURIComponent(upload.callId)}/recording`, {
        method: "PUT", credentials: "include", body: form,
      });
      if (!response.ok) throw new Error("recording upload failed");
      setPendingUpload(null);
      setStatus("Запись готова.");
      await onChanged();
    } catch {
      setPendingUpload(upload);
      setStatus("Сеть недоступна: запись осталась в памяти. Повторите загрузку до срока.");
      setError("Ошибка загрузки записи");
    }
  };

  const start = async () => {
    if (!contact) return;
    setError("");
    try {
      const receipt = await executeCommand(item.id, {
        command_id: crypto.randomUUID(), expected_seq: item.seq, type: "call_start", payload: { contact }, client_at: new Date().toISOString(),
      });
      if (receipt.outcome !== "applied" || !receipt.call_id) {
        setError(receipt.error_code ?? "Не удалось начать звонок");
        return;
      }
      setCallId(receipt.call_id);
      setCallEndSeq(receipt.seq);
      setMuted(false);
      const greeting = phraseText(contact, "greeting");
      setStatus(greeting ? `Абонент: «${greeting}»` : "Звонок: воспроизводится приветствие…");
      try { await new Audio(`/api/v1/items/${encodeURIComponent(item.id)}/contacts/${encodeURIComponent(contact)}/phrases/greeting`).play(); } catch { /* optional phrase */ }
      if (!navigator.mediaDevices?.getUserMedia || typeof MediaRecorder === "undefined") {
        setStatus("Микрофон отсутствует: звонок будет без записи.");
        await onChanged();
        return;
      }
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      streamRef.current = stream;
      const mime = MediaRecorder.isTypeSupported("audio/webm;codecs=opus") ? "audio/webm;codecs=opus" : MediaRecorder.isTypeSupported("audio/webm") ? "audio/webm" : "";
      if (!mime) {
        releaseMicrophone();
        setStatus("MediaRecorder не поддерживается: звонок будет без записи.");
        await onChanged();
        return;
      }
      const chunks: BlobPart[] = [];
      const recorder = new MediaRecorder(stream, { mimeType: mime });
      recordingRef.current = null;
      recordingDoneRef.current = new Promise((resolve) => {
        recorder.onstop = () => {
          const blob = new Blob(chunks, { type: mime.split(";")[0] });
          recordingRef.current = blob;
          releaseMicrophone();
          resolve(blob);
        };
      });
      recorder.ondataavailable = (event) => { if (event.data.size) chunks.push(event.data); };
      recorder.start();
      recorderRef.current = recorder;
      setIsRecording(true);
      setStatus("Идёт запись доклада.");
      await onChanged();
    } catch (cause) {
      releaseMicrophone();
      setError(cause instanceof DOMException && cause.name === "NotAllowedError" ? "Разрешение на микрофон отклонено; можно завершить звонок без записи." : errorMessage(cause));
    }
  };

  const toggleMute = () => {
    const nextMuted = !muted;
    streamRef.current?.getAudioTracks().forEach((track) => { track.enabled = !nextMuted; });
    setMuted(nextMuted);
  };

  const end = async () => {
    if (!callId || callEndSeq === null || (legacy && (!acceptedBy.trim() || !summary.trim()))) return;
    setError("");
    setIsEnding(true);
    try {
      // MediaRecorder.onstop is the authoritative point at which all chunks
      // exist. Awaiting its promise removes the manifest-before-Blob race.
      const recording = await stopRecording();
      let manifest: Record<string, unknown> | null = null;
      if (recording) {
        const hash = await crypto.subtle.digest("SHA-256", await recording.arrayBuffer());
        manifest = {
          sha256: [...new Uint8Array(hash)].map((value) => value.toString(16).padStart(2, "0")).join(""),
          size: recording.size,
          mime: recording.type || "audio/webm",
        };
      }
      const command = callEndCommandRef.current ?? {
        command_id: crypto.randomUUID(), expected_seq: callEndSeq, type: "call_end" as const,
        payload: { call_id: callId, accepted_by: acceptedBy.trim(), summary: summary.trim(), recording: manifest }, client_at: new Date().toISOString(),
      };
      callEndCommandRef.current = command;
      const receipt = await executeCommand(item.id, command);
      if (receipt.outcome !== "applied") {
        setError(receipt.error_code ?? "Не удалось завершить звонок");
        return;
      }
      callEndCommandRef.current = null;
      try { await new Audio(`/api/v1/items/${encodeURIComponent(item.id)}/contacts/${encodeURIComponent(contact)}/phrases/ack`).play(); } catch { /* optional phrase */ }
      setCallId(null);
      setCallEndSeq(null);
      const ack = phraseText(contact, "ack");
      if (recording) {
        await uploadRecording({ callId, blob: recording });
      } else {
        setStatus(ack ? `Абонент: «${ack}» Звонок завершён без записи.` : "Звонок завершён без записи.");
        await onChanged();
      }
    } catch (cause) {
      setError(errorMessage(cause));
      setStatus("Завершение звонка не подтверждено. Повторите завершение: будет отправлена та же команда.");
    } finally {
      setIsEnding(false);
    }
  };

  return (
    <section className="phone-panel">
      <header className="phone-panel-header">
        <div><span className="phone-panel-overline">Встроенный симулятор</span><h3>Телефон Т16Р</h3></div>
        <div className="phone-display"><span>Линия ДДС</span><strong>{contacts.find((candidate) => candidate.key === contact)?.number ?? "—"}</strong></div>
      </header>
      <div className="phone-panel-body">
        <div className="phone-contacts" aria-label="Контакты для вызова">
          <span className="phone-section-label">Кому звоним</span>
          {grouped.map((group) => (
            <div key={group.role} className="phone-contact-group" role="group" aria-label={contactRoleLabels[group.role]}>
              <span className="phone-contact-role">{contactRoleLabels[group.role]}</span>
              {group.contacts.map((candidate) => <button type="button" key={candidate.key} className={contact === candidate.key ? "active" : undefined} disabled={!!callId} onClick={() => setContact(candidate.key)}>{candidate.label}<small>{candidate.number}</small></button>)}
            </div>
          ))}
          {contacts.length === 0 && <p>Контакты не назначены.</p>}
        </div>
        {!callId ? (
          <div className="phone-start-control">
            <span>{incomingActive ? "Линия занята входящим звонком" : <>Громкая связь <b aria-label="включена">●</b></>}</span>
            <button type="button" className="phone-call-button" onClick={() => void start()} disabled={!contact || isEnding || incomingActive}>Вызов</button>
          </div>
        ) : (
          <div className="phone-call-control">
            <div className="phone-call-state"><span className={isRecording ? "phone-recording" : undefined}>●</span>{isRecording ? "Запись доклада" : "Звонок без записи"}</div>
            <button type="button" aria-pressed={muted} onClick={toggleMute} disabled={!isRecording}>{muted ? "Включить микрофон" : "Mute"}</button>
            <label>Кто принял{!legacy && " (необязательно)"}<input value={acceptedBy} onChange={(event) => setAcceptedBy(event.target.value)} /></label>
            <label>Суть сообщения{!legacy && " (необязательно)"}<textarea value={summary} onChange={(event) => setSummary(event.target.value)} /></label>
            <button type="button" className="phone-end-button" onClick={() => void end()} disabled={isEnding || (legacy && (!acceptedBy.trim() || !summary.trim()))}>Завершить</button>
          </div>
        )}
        {pendingUpload && <button type="button" className="phone-retry-button" onClick={() => void uploadRecording(pendingUpload)}>Повторить загрузку записи</button>}
        {item.calls.filter((call) => call.direction !== "incoming").map((call) => <p key={call.id} className="phone-call-history">Запись: {call.recording_state === "expired" ? "не загружена" : call.recording_state}</p>)}
        {status && <p className="notice" aria-live="polite">{status}</p>}
        {error && <p className="error" role="alert">{error}</p>}
      </div>
    </section>
  );
}

type CommsEntry = {
  key: string;
  at: string;
  kind: string;
  from: string;
  text: string;
  mark?: string;
  alarm?: boolean;
};

// CrewCommsPanel is the DDS workplace's «Связь с бригадой» (ADR-031): the
// ringing incoming call, the answered call in progress, the phone for
// outgoing calls, and one time-ordered log of crew reports and calls. A
// ringing call shows only who calls; its words arrive once answered.
function CrewCommsPanel({ item, serverNowMs, disabled, onAnswer, onEndIncoming, phone }: {
  item: DDSItem;
  serverNowMs: number;
  disabled: boolean;
  onAnswer: (eventKey: string) => void;
  onEndIncoming: (callId: string) => void;
  phone?: ReactNode;
}) {
  const contacts = item.card.contacts ?? [];
  const contactLabel = (key?: string) => contacts.find((candidate) => candidate.key === key)?.label ?? key ?? "";
  const contactRole = (key?: string) => contacts.find((candidate) => candidate.key === key)?.role ?? "other";
  const ringing = item.incoming_call && new Date(item.incoming_call.ring_until).getTime() > serverNowMs ? item.incoming_call : null;
  const ringSeconds = ringing ? Math.max(0, Math.ceil((new Date(ringing.ring_until).getTime() - serverNowMs) / 1_000)) : 0;
  const activeIncoming = item.calls.find((call) => call.direction === "incoming" && !call.ended_at);
  const activeIncomingEvent = activeIncoming ? item.events.find((event) => event.key === activeIncoming.event_key) : undefined;
  const finished = item.state === "closed" || item.state === "interrupted";

  const entries: CommsEntry[] = [];
  for (const event of item.events) {
    if (event.delivery === "notice") {
      entries.push({ key: `e-${event.key}`, at: event.delivered_at, kind: contactRole(event.from) === "crew" ? "Доклад бригады" : "Сообщение", from: contactLabel(event.from), text: event.text, mark: event.late ? "с опозданием" : undefined });
    } else if (event.delivery === "phone_incoming") {
      const isRinging = ringing?.event_key === event.key;
      const mark = event.answered ? "принят" : isRinging ? "звонит" : finished ? "не отвечен" : "пропущен";
      entries.push({ key: `e-${event.key}`, at: event.delivered_at, kind: "Входящий звонок", from: contactLabel(event.from), text: event.answered ? event.text : "", mark, alarm: !event.answered && !isRinging });
    }
  }
  for (const call of item.calls) {
    if (call.direction === "incoming") continue;
    entries.push({ key: `c-${call.id}`, at: call.started_at, kind: "Исходящий звонок", from: contactLabel(call.contact_key), text: call.summary ?? "", mark: call.ended_at ? undefined : "идёт" });
  }
  entries.sort((a, b) => new Date(a.at).getTime() - new Date(b.at).getTime());

  return (
    <aside className="dds-comms-panel" aria-label="Связь с бригадой">
      <h3>Связь с бригадой</h3>
      {ringing && !finished && (
        <div className="dds-incoming-call" role="alert">
          <div>
            <span className="dds-incoming-label">Входящий звонок · {contactRoleLabels[contactRole(ringing.from)] ?? ""}</span>
            <strong>{contactLabel(ringing.from)}</strong>
            <span className="dds-incoming-timer">Звонит ещё {ringSeconds} с</span>
          </div>
          <button type="button" className="dds-answer-button" disabled={disabled || !!activeIncoming || item.calls.some((call) => !call.ended_at)} onClick={() => onAnswer(ringing.event_key)}>Ответить</button>
        </div>
      )}
      {activeIncoming && (
        <div className="dds-incoming-active">
          <span className="dds-incoming-label">Разговор · {contactLabel(activeIncoming.contact_key)}</span>
          <p>{activeIncomingEvent?.text ? `«${activeIncomingEvent.text}»` : "…"}</p>
          <button type="button" className="dds-hangup-button" disabled={disabled} onClick={() => onEndIncoming(activeIncoming.id)}>Завершить разговор</button>
        </div>
      )}
      {phone}
      <ol className="dds-comms-log" aria-label="Журнал связи">
        {entries.map((entry) => (
          <li key={entry.key} className={entry.alarm ? "dds-comms-alarm" : undefined}>
            <div className="dds-comms-meta">
              <strong>{entry.kind}</strong> · {entry.from} · {formatDateTime(entry.at)}{entry.mark ? ` · ${entry.mark}` : ""}
            </div>
            {entry.text && <p>{entry.text}</p>}
          </li>
        ))}
        {entries.length === 0 && <li className="dds-comms-empty">Докладов и звонков пока нет.</li>}
      </ol>
    </aside>
  );
}

// Statuses whose comment the DDS guide makes mandatory (памятка, стр.
// 21–23): the reason and where the information was passed. This is the
// public rule, not the scenario's reference; the server enforces it per
// the card's own workflow snapshot.
const commentRequiredStatuses = new Set(["not_accepted", "refused", "completed_without_team"]);

type StatusEntry = { status: string; comment: string; at: string };

function statusHistory(item: DDSItem): StatusEntry[] {
  return item.actions
    .filter((action) => action.type === "set_status" && action.accepted)
    .map((action) => {
      const payload = (action.payload ?? {}) as { status?: string; comment?: string };
      return { status: payload.status ?? "", comment: payload.comment ?? "", at: action.server_at };
    });
}

// ServiceStatusBlock is the trainee's own service in the card's service
// list (ADR-030): its current reaction status, the ▾ history of saved
// statuses with their comments, and the ✎ pencil that saves the next
// status together with its comment.
function ServiceStatusBlock({ item, terminalStatuses, disabled, onSave, compact }: {
  compact?: boolean;
  item: DDSItem;
  terminalStatuses: string[];
  disabled: boolean;
  onSave: (status: string, comment: string) => void;
}) {
  const history = statusHistory(item);
  const last = history[history.length - 1];
  const [editing, setEditing] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const [status, setStatus] = useState("");
  const [text, setText] = useState("");
  const commentRequired = commentRequiredStatuses.has(status);
  const closes = terminalStatuses.includes(status);
  const canEdit = !disabled && item.allowed_transitions.length > 0;
  const save = (event: FormEvent) => {
    event.preventDefault();
    if (!status || (commentRequired && !text.trim())) return;
    onSave(status, text.trim());
    setEditing(false);
    setStatus("");
    setText("");
  };
  return (
    <div className="dds-service-block">
      <div className="dds-service-block-head">
        <span>{reactionLabel(item.reaction)}{last && !compact ? ` · ${formatDateTime(last.at)}` : ""}</span>
        {history.length > 0 && (
          <button type="button" className="dds-service-history-toggle" aria-expanded={expanded} onClick={() => setExpanded(!expanded)}>
            {expanded ? "▴ Скрыть историю" : "▾ История статусов"}
          </button>
        )}
        {canEdit && !editing && (
          <button type="button" className="dds-service-pencil" aria-label="Проставить статус реагирования" onClick={() => setEditing(true)}>✎</button>
        )}
      </div>
      {expanded && (
        <ol className="dds-service-history">
          {history.map((entry, index) => (
            <li key={index}><strong>{reactionLabel(entry.status as DDSItem["reaction"])}</strong> · {formatDateTime(entry.at)}{entry.comment ? `: ${entry.comment}` : ""}</li>
          ))}
        </ol>
      )}
      {editing && (
        <form className="dds-status-form" onSubmit={save}>
          <label>Статус реагирования
            <select value={status} onChange={(event) => setStatus(event.target.value)}>
              <option value="">Выберите статус</option>
              {item.allowed_transitions.map((next) => <option key={next} value={next}>{reactionLabel(next)}</option>)}
            </select>
          </label>
          <label>Комментарий{commentRequired ? " (обязателен: причина и куда передана информация)" : ""}
            <textarea value={text} onChange={(event) => setText(event.target.value)} />
          </label>
          {closes && <p className="notice">Сохранение этого статуса закроет карточку для редактирования. Внесите в комментарий всю информацию заранее.</p>}
          <div className="dds-status-form-actions">
            <button type="submit" disabled={!status || (commentRequired && !text.trim())}>Сохранить</button>
            <button type="button" onClick={() => { setEditing(false); setStatus(""); setText(""); }}>Отмена</button>
          </div>
        </form>
      )}
    </div>
  );
}

function CardStatusBadge({ status }: { status: DDSItem["card_status"] }) {
  if (!status) return <>—</>;
  return <span className={`card-status-badge${cardStatusAlarm(status) ? " card-status-alarm" : ""}`}>{cardStatusLabel(status)}</span>;
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
