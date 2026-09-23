import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { useOutletContext } from "react-router-dom";
import { executeCommand, type Command, type Receipt } from "../../api/commands";
import { errorMessage } from "../../api/errors";
import { useEventStream } from "../../api/realtime";
import type { Me } from "../../api/useMe";
import { itemQueryKey, myItemsQueryKey, myRunQueryKey, useItem, useMyItems, useMyRun, type CardView, type Item } from "../../api/workplace";
import { availableLocalStorage, clearPending, loadPending, savePending, type PendingCommand } from "../../commands/pending";
import { IncidentCard } from "../../components/IncidentCard";
import { formatDateTime } from "../../format";
import { reactionLabel } from "../../labels";
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
  const [queueSearch, setQueueSearch] = useState("");
  // The run disappears from /my/run the instant its last item closes
  // (ActiveRunByUser only ever returns an active run) — but the trainee
  // must still be able to see that just-closed card and send a
  // control_report against it (slice-4-plan.md's C10: "после close
  // оставить только что закрытую карточку в UI"). lastItemId keeps
  // fetching the same item after the run itself is gone, for the rest of
  // this mount only — a reload starts over with nothing to show, which
  // is explicitly out of scope until a later slice's history screen.
  const [lastItemId, setLastItemId] = useState("");
  // An assigned trainee lands on the queue, not directly inside a card.
  // selectedItemId is therefore deliberately empty until they open a row.
  // lastItemId only supports the post-close control report described below.
  const itemId = selectedItemId || lastItemId;
  const item = useItem(itemId, (workstationMatches && !!run.data && !!selectedItemId) || (!run.data && itemId === lastItemId));

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
    if (!lastItemId) return <Waiting me={me} />;
    return (
      <section>
        {item.isPending && <p>Загрузка карточки…</p>}
        {item.isError && <p className="error">{errorMessage(item.error)}</p>}
		{item.data && (item.data.exercise_type === "operator112_intake" ? <Operator112Workplace key={item.data.id} me={me} item={item.data as unknown as IntakeItem} /> : <ItemWorkplace key={item.data.id} me={me} item={item.data as DDSItem} />)}
      </section>
    );
  }

  return (
    <section className="trainee-workplace">
      <header className="workplace-header">
        <div>
		  <p className="workplace-kicker">Рабочее место {run.data.exercise_type === "operator112_intake" ? "112" : "ДДС"} · РМ-{run.data.workstation_no}</p>
		  <h1>{selectedItemId ? run.data.exercise_type === "operator112_intake" ? "Входящий вызов" : "Карточка происшествия" : run.data.lesson.title}</h1>
        </div>
        <dl className="workplace-facts">
          <dt>В очереди</dt><dd>{run.data.queue_left}</dd>
          <dt>Режим</dt><dd>{run.data.mode === "intro" ? "ознакомительный" : "тренировка"}</dd>
        </dl>
      </header>
      {run.data.lesson.state === "stopped" && (
        <p role="alert" className="notice">Занятие остановлено преподавателем{run.data.lesson.stop_reason ? `: ${run.data.lesson.stop_reason}` : ""}. Открытые карточки прерываются фоново.</p>
      )}
      {!workstationMatches && (
        <p role="alert" className="error">Занятие назначено на РМ-{run.data.workstation_no}. Войдите на этом рабочем месте.</p>
      )}
      {workstationMatches && items.isError && <p className="error">{errorMessage(items.error)}</p>}
      {workstationMatches && !selectedItemId && items.data && (
        <IncidentQueue
          items={items.data}
          search={queueSearch}
          onSearch={setQueueSearch}
          onOpen={setSelectedItemId}
        />
      )}
      {workstationMatches && selectedItemId && (
        <>
		  <button type="button" className="back-to-queue" onClick={() => setSelectedItemId("")}>← К списку {run.data.exercise_type === "operator112_intake" ? "вызовов" : "происшествий"}</button>
          {item.isPending && <p>Загрузка карточки…</p>}
          {item.isError && <p className="error">{errorMessage(item.error)}</p>}
		  {item.data && (item.data.exercise_type === "operator112_intake" ? <Operator112Workplace key={item.data.id} me={me} item={item.data as unknown as IntakeItem} /> : <ItemWorkplace key={item.data.id} me={me} item={item.data as DDSItem} />)}
        </>
      )}
    </section>
  );
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

function IncidentQueue({
  items,
  search,
  onSearch,
  onOpen,
}: {
  items: NonNullable<ReturnType<typeof useMyItems>["data"]>;
  search: string;
  onSearch: (value: string) => void;
  onOpen: (id: string) => void;
}) {
  const needle = search.trim().toLocaleLowerCase("ru-RU");
  const visibleItems = needle === ""
    ? items
    : items.filter((candidate) => [candidate.card_number, candidate.incident_type, candidate.address_short]
      .filter(Boolean)
      .some((value) => value?.toLocaleLowerCase("ru-RU").includes(needle)));

  return (
    <section className="incident-queue" aria-labelledby="queue-title">
      <header className="incident-queue-header">
        <div>
          <h2 id="queue-title">Список происшествий</h2>
          <p>{items.length === 0 ? "Новых карточек пока нет." : `Показано: ${visibleItems.length} из ${items.length}`}</p>
        </div>
        <label className="queue-search">
          <span>Поиск происшествий</span>
          <input
            type="search"
            value={search}
            placeholder="Номер, тип или адрес"
            onChange={(event) => onSearch(event.target.value)}
          />
        </label>
      </header>
      <div className="incident-queue-table-wrap">
        <table>
          <thead>
            <tr>
              <th>Номер</th>
              <th>Время</th>
              <th>Тип происшествия</th>
              <th>Адрес</th>
              <th>Статус службы</th>
              <th aria-label="Открыть карточку" />
            </tr>
          </thead>
          <tbody>
            {visibleItems.map((candidate) => (
              <tr key={candidate.id} className={candidate.state === "offered" ? "incident-queue-new" : undefined}>
                <td><strong>№ {candidate.card_number}</strong>{candidate.state === "offered" && <span className="queue-new-mark">новая</span>}</td>
                <td>{formatQueueTime(candidate.offered_at)}</td>
                <td>{candidate.incident_type ?? "—"}</td>
                <td>{candidate.address_short ?? "—"}</td>
				<td>{candidate.exercise_type === "operator112_intake" ? candidate.call_status === "ringing" ? "Ожидает ответа" : candidate.call_status === "connected" ? "Разговор" : candidate.dispatched ? "Направлена" : "Разговор окончен" : reactionLabel(candidate.reaction)}{candidate.interruptions.length > 0 && " · ⚠"}</td>
                <td><button type="button" className="queue-open" onClick={() => onOpen(candidate.id)}>Открыть карточку № {candidate.card_number}</button></td>
              </tr>
            ))}
            {visibleItems.length === 0 && (
              <tr><td colSpan={6} className="queue-empty">По этому запросу происшествий нет.</td></tr>
            )}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function formatQueueTime(value: string): string {
  return new Date(value).toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

function ItemWorkplace({ me, item }: { me: Me; item: DDSItem }) {
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
    <section className="dds-workplace">
      {item.interruptions.length > 0 && (
        <p role="alert" className="notice">
          Карточка была прервана перезапуском сервера ({item.interruptions.length}×, последний раз {formatDateTime(item.interruptions[item.interruptions.length - 1].detected_at)}). Норматив времени по ней не учитывается.
        </p>
      )}
      <section className="dds-item-status" aria-label="Статус обработки карточки">
        <div><span>Статус службы</span><strong>{reactionLabel(item.reaction)}</strong></div>
        <div><span>Выдана</span><strong>{formatDateTime(item.offered_at)}</strong></div>
        <div><span>Открыть</span><strong>{remaining(item.deadlines.open_at, clockAnchor.server + clientNow - clockAnchor.client)}</strong></div>
        <div><span>Первичное решение</span><strong>{remaining(item.deadlines.primary_at, clockAnchor.server + clientNow - clockAnchor.client)}</strong></div>
        {item.deadlines.complete_at && <div><span>Завершить</span><strong>{remaining(item.deadlines.complete_at, clockAnchor.server + clientNow - clockAnchor.client)}</strong></div>}
      </section>
      <IncidentCard card={item.card} />
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
          {!open && <PhonePanel item={item} onChanged={refresh} />}
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

// PhonePanel is intentionally a small browser simulator, not a SIP client.
// A not-yet-uploaded recording stays in this component's memory only; a tab
// reload therefore has the explicitly documented "missing recording" outcome.
function PhonePanel({ item, onChanged }: { item: DDSItem; onChanged: () => Promise<void> }) {
  const contacts = item.card.contacts ?? [];
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
      setStatus("Звонок: воспроизводится приветствие…");
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
    if (!callId || callEndSeq === null || !acceptedBy.trim() || !summary.trim()) return;
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
      if (recording) {
        await uploadRecording({ callId, blob: recording });
      } else {
        setStatus("Звонок завершён без записи.");
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
          {contacts.map((candidate) => <button type="button" key={candidate.key} className={contact === candidate.key ? "active" : undefined} disabled={!!callId} onClick={() => setContact(candidate.key)}>{candidate.label}<small>{candidate.number}</small></button>)}
          {contacts.length === 0 && <p>Контакты не назначены.</p>}
        </div>
        {!callId ? (
          <div className="phone-start-control">
            <span>Громкая связь <b aria-label="включена">●</b></span>
            <button type="button" className="phone-call-button" onClick={() => void start()} disabled={!contact || isEnding}>Вызов</button>
          </div>
        ) : (
          <div className="phone-call-control">
            <div className="phone-call-state"><span className={isRecording ? "phone-recording" : undefined}>●</span>{isRecording ? "Запись доклада" : "Звонок без записи"}</div>
            <button type="button" aria-pressed={muted} onClick={toggleMute} disabled={!isRecording}>{muted ? "Включить микрофон" : "Mute"}</button>
            <label>Кто принял<input value={acceptedBy} onChange={(event) => setAcceptedBy(event.target.value)} /></label>
            <label>Суть сообщения<textarea value={summary} onChange={(event) => setSummary(event.target.value)} /></label>
            <button type="button" className="phone-end-button" onClick={() => void end()} disabled={isEnding || !acceptedBy.trim() || !summary.trim()}>Завершить</button>
          </div>
        )}
        {pendingUpload && <button type="button" className="phone-retry-button" onClick={() => void uploadRecording(pendingUpload)}>Повторить загрузку записи</button>}
        {item.calls.map((call) => <p key={call.id} className="phone-call-history">Запись: {call.recording_state === "expired" ? "не загружена" : call.recording_state}</p>)}
        {status && <p className="notice" aria-live="polite">{status}</p>}
        {error && <p className="error" role="alert">{error}</p>}
      </div>
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
