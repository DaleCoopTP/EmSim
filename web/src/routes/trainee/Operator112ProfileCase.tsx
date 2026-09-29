import { useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { executeCommand, type Command, type Receipt } from "../../api/commands";
import { errorMessage } from "../../api/errors";
import type { Me } from "../../api/useMe";
import { itemQueryKey, myItemsQueryKey, myRunQueryKey } from "../../api/workplace";
import { availableLocalStorage, clearPending, loadPending, savePending, type PendingCommand } from "../../commands/pending";
import { BellIcon, CloseIcon, GlobeIcon, HangupIcon, HelpIcon, LinkIcon, MapIcon, MessageIcon, PhoneIcon, PinIcon, PlusIcon, SmsIcon, StopwatchIcon, TranslateIcon } from "../../components/Arm112Icons";
import { formatDateTime } from "../../format";
import { armCardNumber, armOperatorNumber, armShortName } from "../../arm112Number";
import { IncomingCallDialog } from "./Arm112Main";
import { CallerChat } from "./CallerChat";
import { applicantStatuses, serviceNames, serviceTiles } from "../../intakeServices";
import { profileFieldVisible } from "../../intakeProfile";
import type { IntakeCard, IntakeCatalog, IntakeField, IntakeItem, IntakeProfileAnswer } from "./Operator112Workplace";

const empty: IntakeField = { state: "unanswered" };
const channels = ["МТС", "Мегафон", "Билайн", "Теле2", "Мобильное приложение", "Стационарный телефон"];
const phonePlaceholder = "+7 (   )   -   -";
// The elapsed timer turns red like the reference screen. It is a visual cue
// only: case 112-3 has no time standard and the value is never scored.
const timerAlertSeconds = 180;
const unavailable = "Недоступно в учебной карточке";

const invalidMessage = "Карточка не сохранена: поля, выделенные красным, заполнены некорректно.";

const errorLabels: Record<string, string> = {
  stale_seq: "Карточка изменилась. Проверьте новые данные и повторите действие.",
  transition_not_allowed: "Это действие сейчас недоступно.",
  invalid_payload: "Карточка не сохранена: сервер отклонил данные. Проверьте поля карточки.",
  item_closed: "Обработка уже завершена.",
  lesson_stopped: "Занятие остановлено преподавателем.",
};

const knownValue = (field: IntakeField | undefined) => field?.state === "known" ? field.value ?? "" : "";
const fromText = (value: string): IntakeField => value ? { state: "known", value } : empty;
const profileTitle = (name: string) => name.split(" · ")[0];

// The server rejects a text value with surrounding whitespace
// (training.ValidIntakeCard, validProfileAnswer). A trailing space or a
// newline left in the description is invisible, so the draft is trimmed
// on save; a value that is only whitespace becomes unanswered.
const trimField = (field: IntakeField): IntakeField => {
  if (field.state !== "known" || field.value === undefined) return field;
  const value = field.value.trim();
  return value ? { state: "known", value } : empty;
};
const isField = (value: unknown): value is IntakeField => typeof value === "object" && value !== null && "state" in value;
const trimFields = <T extends object>(fields: T): T =>
  Object.fromEntries(Object.entries(fields).map(([key, value]) => [key, isField(value) ? trimField(value) : value])) as T;
function normalizeDraft(draft: IntakeCard, catalog: IntakeCatalog | undefined): IntakeCard {
  const card = { ...trimFields(draft), address: trimFields(draft.address) };
  if (!draft.profiles) return card;
  card.profiles = Object.fromEntries(Object.entries(draft.profiles).map(([id, profile]) => {
    const textFields = new Set(catalog?.profiles.find((definition) => definition.id === id)?.fields
      .filter((field) => field.kind === "text").map((field) => field.id));
    const answers = Object.fromEntries(Object.entries(profile.answers).map(([fieldID, answer]) => {
      if (!textFields.has(fieldID) || answer.state !== "known") return [fieldID, answer];
      const value = (answer.value ?? "").trim();
      return [fieldID, value ? { state: "known", value } : { state: "unanswered" }];
    }));
    return [id, { ...profile, answers }];
  }));
  return card;
}

// invalidFields mirrors the server's own per-field limits
// (training.ValidIntakeCard) on an already trimmed draft, so a value the
// server would reject is shown in red before anything is sent. Keys are
// the card's own field names, address fields as "address.<name>".
const fieldLimits: Partial<Record<keyof IntakeCard, number>> = { complaint: 1999, channel: 100 };
const runeCount = (value: string) => Array.from(value).length;
function invalidFields(card: IntakeCard): Set<string> {
  const invalid = new Set<string>();
  const check = (key: string, field: unknown, max: number) => {
    if (!isField(field)) return;
    const value = field.value ?? "";
    const bad = field.state === "known" ? value === "" || runeCount(value) > max || value.trim() !== value : value !== "";
    if (bad) invalid.add(key);
  };
  for (const [key, field] of Object.entries(card)) check(key, field, fieldLimits[key as keyof IntakeCard] ?? 1000);
  for (const [key, field] of Object.entries(card.address)) check(`address.${key}`, field, 1000);
  return invalid;
}

// Underlined ARM field: caption above, value on the line. The "?" toggle keeps
// the domain distinction "заявитель не знает" without the reference layout
// losing its plain look.
function ArmField({ label, field, onChange, disabled, className, placeholder, invalid, children }: {
  label: string; field: IntakeField | undefined; onChange: (value: IntakeField) => void; disabled: boolean;
  className?: string; placeholder?: string; invalid?: boolean; children?: ReactNode;
}) {
  const unknown = field?.state === "unknown";
  return <div className={`arm112-field${unknown ? " is-unknown" : ""}${invalid ? " is-invalid" : ""}${className ? ` ${className}` : ""}`}>
    <label><span>{label}:</span>
      <input aria-label={`${label}: значение`} aria-invalid={invalid || undefined} value={knownValue(field)} maxLength={1000} disabled={disabled || unknown}
        placeholder={unknown ? "неизвестно" : placeholder} onChange={(event) => onChange(fromText(event.target.value))} />
    </label>
    <button type="button" className="arm112-unknown" aria-label={`${label}: неизвестно`} aria-pressed={unknown} disabled={disabled}
      title="Заявитель не знает" onClick={() => onChange(unknown ? empty : { state: "unknown" })}>?</button>
    {children}
  </div>;
}

function PhoneBox({ label, field, aon, onChange, disabled, icons, copyAon }: {
  label: string; field?: IntakeField; aon: string; onChange?: (value: IntakeField) => void; disabled: boolean; icons: ReactNode; copyAon?: boolean;
}) {
  return <>
    <div className="arm112-phone-side" aria-hidden="true"><PhoneIcon size={22} /><SmsIcon size={17} /></div>
    <div className="arm112-phone">
      <div className="arm112-phone-head"><span>{label}</span><span className="arm112-phone-icons" aria-hidden="true">{icons}</span></div>
      {onChange ? <input aria-label={`${label}: значение`} value={knownValue(field)} placeholder={phonePlaceholder} maxLength={40} disabled={disabled}
        onChange={(event) => onChange(fromText(event.target.value))} />
        : <output aria-label={label} className={aon ? "" : "is-empty"}>{aon || phonePlaceholder}</output>}
      {copyAon && onChange && <button type="button" className="arm112-aon" disabled={disabled || !aon} onClick={() => onChange({ state: "known", value: aon })}>АОН</button>}
    </div>
  </>;
}

// acceptOnOpen: the main screen's "Принять" was pressed for this call, so the
// card opens and answers it without a second click.
export function Operator112ProfileCase({ me, item, onClose, acceptOnOpen }: { me: Me; item: IntakeItem; onClose?: () => void; acceptOnOpen?: boolean }) {
  const client = useQueryClient();
  const [storage] = useState(() => availableLocalStorage());
  const [pending, setPending] = useState<PendingCommand | null>(() => storage ? loadPending(storage, me.user.id, item.id) : null);
  const [receipt, setReceipt] = useState<Receipt | null>(null);
  // The rejected send_caller_message's own error label, for CallerChat to
  // show next to the still-typed text; cleared by the next chat send.
  const [chatRejection, setChatRejection] = useState<string | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [draft, setDraft] = useState<IntakeCard>(item.card);
  // Fields the last save attempt found invalid; cleared as soon as the
  // operator edits the draft again.
  const [invalid, setInvalid] = useState<Set<string>>(() => new Set());
  const [selectedServices, setSelectedServices] = useState<string[]>([]);
  const [reviewReason, setReviewReason] = useState("");
  const [servicesOpen, setServicesOpen] = useState(false);
  const [outcomeIntent, setOutcomeIntent] = useState<"no_contact" | "call_dropped" | null>(null);
  // "Принять" is two commands: open, then answer_incoming sent from the
  // open's own receipt (its seq), so the operator clicks once.
  const [accepting, setAccepting] = useState(false);
  const acceptingRef = useRef(false);
  // The incoming-call window's × opens the card without answering and hides
  // the window: the line block then offers "ответить", and "нет контакта"
  // (allowed only while the opened call still rings) becomes reachable.
  const [callWindowHidden, setCallWindowHidden] = useState(false);
  const [clientNow, setClientNow] = useState(Date.now);
  const [clock] = useState(() => ({ client: Date.now(), server: new Date(item.server_time).getTime() }));
  const chatStorageKey = `emsim:112:chat-open:${item.id}`;
  const [chatOpen, setChatOpen] = useState(() => {
    try {
      const saved = storage?.getItem(chatStorageKey);
      if (saved === "open") return true;
      if (saved === "closed") return false;
    } catch { /* falls through to the default below */ }
    return true; // open by default the first time the chat becomes available
  });
  const toggleChat = () => setChatOpen((current) => {
    const next = !current;
    try { storage?.setItem(chatStorageKey, next ? "open" : "closed"); } catch { /* per-viewer convenience only */ }
    return next;
  });
  const state = item.intake_state;
  const catalog = state.catalog;
  const terminal = item.state === "closed" || item.state === "interrupted";
  const isCall = state.mode === "full_case";
  const notifyFlow = state.finale === "notify";
  const notified = notifyFlow && state.notified;
  const opened = !terminal && item.state !== "offered";
  const ringingCall = isCall && state.call_status === "ringing";
  const bodyReady = opened && !ringingCall;
  const editable = bodyReady && !notified;
  const cardSignature = JSON.stringify(item.card);
  const dirty = JSON.stringify(draft) !== cardSignature;
  const suggested = state.suggested_services ?? [];
  const reviewed = notifyFlow ? item.notification?.services.map((entry) => entry.service_code) : state.service_review?.selected;
  const serverNow = clock.server + clientNow - clock.client;
  const until = terminal && item.closed_at ? new Date(item.closed_at).getTime() : serverNow;
  const elapsed = Math.max(0, Math.floor((until - new Date(item.offered_at).getTime()) / 1000));

  const resetSelection = () => {
    setSelectedServices(state.service_review?.selected ?? suggested.map((entry) => entry.service_code));
    setReviewReason(state.service_review?.reason ?? "");
  };
  // Keyed off the card's own content (not item.seq, which every accepted
  // command bumps, including a chat message or an asynchronous caller
  // reply's own SSE-triggered refetch) — otherwise those unrelated
  // updates would silently discard whatever the operator is mid-typing
  // in the card.
  useEffect(() => {
    setDraft(item.card);
    resetSelection();
  }, [cardSignature]); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { const timer = window.setInterval(() => setClientNow(Date.now()), 1000); return () => window.clearInterval(timer); }, []);


  const refresh = async () => { await Promise.all([
    client.invalidateQueries({ queryKey: itemQueryKey(item.id) }),
    client.invalidateQueries({ queryKey: myItemsQueryKey }),
    client.invalidateQueries({ queryKey: myRunQueryKey }),
  ]); };
  const deliver = async (value: PendingCommand) => {
    setError(null);
    try {
      const response = await executeCommand(value.item_id, value.command);
      setReceipt(response);
      if (value.command.type === "send_caller_message") {
        setChatRejection(response.outcome === "rejected" ? errorLabels[response.error_code ?? ""] ?? response.error_code ?? "отклонено" : null);
      }
      await refresh();
      if (storage) clearPending(storage, me.user.id, value.item_id, value.command.command_id);
      setPending(null);
      if (acceptingRef.current) {
        const answer = value.command.type === "open" && response.outcome === "applied";
        acceptingRef.current = false;
        setAccepting(false);
        if (answer) dispatch("answer_incoming", {}, response.seq);
      }
    } catch (cause) { setError(cause); acceptingRef.current = false; setAccepting(false); }
  };
  function dispatch(type: Command["type"], payload: Record<string, unknown>, expectedSeq: number) {
    const command: Command = { command_id: crypto.randomUUID(), expected_seq: expectedSeq, type, payload, client_at: new Date().toISOString() };
    const value = { item_id: item.id, command };
    try {
      if (storage) savePending(storage, me.user.id, item.id, command);
      setPending(value);
      void deliver(value);
    } catch (cause) { setError(cause); }
  }
  const send = (type: Command["type"], payload: Record<string, unknown>) => {
    if (!pending) dispatch(type, payload, item.seq);
  };
  const accept = () => {
    if (pending) return;
    if (item.state === "offered") { acceptingRef.current = true; setAccepting(true); }
    send(item.state === "offered" ? "open" : "answer_incoming", {});
  };
  useEffect(() => {
    if (pending) queueMicrotask(() => { void deliver(pending); });
    else if (acceptOnOpen && isCall && !terminal && (item.state === "offered" || ringingCall)) queueMicrotask(accept);
  }, []); // eslint-disable-line react-hooks/exhaustive-deps
  const clearInvalid = (key: string) => setInvalid((current) => {
    if (!current.has(key)) return current;
    const next = new Set(current);
    next.delete(key);
    return next;
  });
  const update = (key: keyof IntakeCard, field: IntakeField) => { clearInvalid(key); setDraft((current) => ({ ...current, [key]: field })); };
  const updateAddress = (key: keyof IntakeCard["address"], field: IntakeField) => { clearInvalid(`address.${key}`); setDraft((current) => ({ ...current, address: { ...current.address, [key]: field } })); };
  // Changing a "Где"-like answer hides the other branch's questions; their
  // answers are cleared here, since the server accepts a hidden field only
  // while it is unanswered.
  const updateAnswer = (profileID: string, fieldID: string, answer: IntakeProfileAnswer) => setDraft((current) => {
    const answers = { ...current.profiles![profileID].answers, [fieldID]: answer };
    for (const field of catalog?.profiles.find((profile) => profile.id === profileID)?.fields ?? []) {
      if (field.kind !== "shared" && answers[field.id] && answers[field.id].state !== "unanswered" && !profileFieldVisible(field, answers)) {
        answers[field.id] = { state: "unanswered" };
      }
    }
    return { ...current, profiles: { ...current.profiles, [profileID]: { ...current.profiles![profileID], answers } } };
  });
  const save = (event: FormEvent) => {
    event.preventDefault();
    const normalized = normalizeDraft(draft, catalog);
    const problems = invalidFields(normalized);
    setDraft(normalized);
    setInvalid(problems);
    if (problems.size === 0) send("save_intake_draft", { draft: normalized });
  };
  const toggleFlag = (key: "no_on_site" | "no_access") => update(key, draft[key]?.state === "known" ? empty : { state: "known", value: "yes" });
  const clearAddress = () => setDraft((current) => ({ ...current, address: Object.fromEntries(Object.keys(current.address).map((key) => [key, empty])) as IntakeCard["address"] }));
  const allServices = item.available_service_codes ?? [];
  const selectionChanged = selectedServices.length !== suggested.length || selectedServices.some((code) => !suggested.some((entry) => entry.service_code === code));
  const reviewBlocked = !editable || !state.has_saved_draft || dirty || !!pending;
  const removeType = (id: string) => {
    if (window.confirm("Убрать тип происшествия? Ответы его карты сохранятся для восстановления, но не войдут в итоговую карточку и предложение служб.")) send("remove_incident_type", { type_id: id });
  };
  const address = draft.address;
  const addressSummary = [address.country, address.region, address.city, address.street, address.house, address.building]
    .map(knownValue).filter(Boolean).join(", ");
  const barServices = reviewed ?? suggested.map((entry) => entry.service_code);
  const statusValue = draft.applicant_status.state === "unknown" ? "unknown" : knownValue(draft.applicant_status);
  const channelValue = draft.channel.state === "unknown" ? "unknown" : knownValue(draft.channel);
  const rejection = receipt?.outcome === "rejected" ? errorLabels[receipt.error_code ?? ""] ?? receipt.error_code : null;
  // A rejection is also repeated next to the "сохранить" button (without a
  // second alert role): the feedback block sits below the form and is easy
  // to miss.
  // "нет контакта" / "срыв звонка" (instruction fig. 11): one panel for
  // every mode; a card without a call shows them disabled, as the ARM does.
  const outcomeButtons = <div className="arm112-strip arm112-outcomes">
    <button type="button" disabled={!isCall || terminal || !!pending || item.state !== "opened" || state.call_status !== "ringing"}
      title={isCall ? "Заявитель не отвечает на принятый вызов" : "В учебной карточке без разговора не используется"} onClick={() => setOutcomeIntent("no_contact")}>нет контакта</button>
    <button type="button" disabled={!isCall || terminal || !!pending || (state.call_status !== "connected" && state.call_status !== "held") || notified}
      title={isCall ? "Связь с заявителем прервалась" : "В учебной карточке без разговора не используется"} onClick={() => setOutcomeIntent("call_dropped")}>срыв звонка</button>
  </div>;
  const operator = [armOperatorNumber(me.user.id), me.workstation ? `АРМ ${me.workstation.number}` : null, armShortName(me.user.full_name)].filter(Boolean).join(", ");

  return <section className="arm112 intake-profile-case">
    <header className="arm112-top">
      <div className="arm112-line">
        <span className="arm112-hangup" aria-hidden="true"><HangupIcon size={26} /></span>
        {isCall ? <div className="arm112-line-state">
          <span>{state.call_status === "ringing" ? "входящий вызов" : state.call_status === "connected" ? "разговор" : state.call_status === "held" ? "на удержании" : "разговор завершён"}</span>
          <div>
            {item.state === "opened" && state.call_status === "ringing" && callWindowHidden && <button type="button" disabled={!!pending} onClick={() => send("answer_incoming", {})}>ответить</button>}
            {state.call_status === "connected" && !terminal && <button type="button" disabled={!!pending} onClick={() => send("hold_incoming", {})}>удержать</button>}
            {state.call_status === "held" && !terminal && <button type="button" disabled={!!pending} onClick={() => send("resume_incoming", {})}>вернуться к разговору</button>}
            {(state.call_status === "connected" || state.call_status === "held") && !terminal && <button type="button" disabled={!!pending} onClick={() => send("end_incoming", {})}>завершить разговор</button>}
            {state.caller_mode === "free_text" && bodyReady && <button type="button" aria-pressed={chatOpen} onClick={toggleChat}>{chatOpen ? "скрыть чат" : "чат с заявителем"}</button>}
          </div>
        </div> : <div className="arm112-line-state"><span>не подключен</span></div>}
      </div>
      <PhoneBox label="АОН" aon={item.card.aon} disabled icons={<><HelpIcon size={15} /><PinIcon size={15} /><GlobeIcon size={15} /></>} />
      <PhoneBox label="предоставленный" field={draft.provided_phone} aon={item.card.aon} disabled={!editable} copyAon icons={<GlobeIcon size={15} />} onChange={(value) => update("provided_phone", value)} />
      <PhoneBox label="телефон на место" field={draft.on_site_phone ?? empty} aon={item.card.aon} disabled={!editable} copyAon icons={<GlobeIcon size={15} />} onChange={(value) => update("on_site_phone", value)} />
      <div className="arm112-records">
        <div><button type="button" disabled title={unavailable}>записи звонков</button><button type="button" disabled title={unavailable}>список SMS</button></div>
        <select className="arm112-plain" aria-label="Канал связи" value={channelValue} disabled={!editable}
          onChange={(event) => update("channel", event.target.value === "unknown" ? { state: "unknown" } : fromText(event.target.value))}>
          <option value="">канал связи</option>
          {channels.map((channel) => <option key={channel} value={channel}>{channel}</option>)}
          {channelValue && channelValue !== "unknown" && !channels.includes(channelValue) && <option value={channelValue}>{channelValue}</option>}
          <option value="unknown">не знает</option>
        </select>
      </div>
      <div className="arm112-incident">
        <strong title={`Карточка ${item.card.number}`}>Карточка {armCardNumber(item.card.number)}</strong>
        <span>Зарег. {formatDateTime(item.offered_at)}</span>
        <span>Опер. {operator}</span>
        {!isCall && <span className="arm112-incident-note">Учебная карточка без разговора</span>}
      </div>
      <div className={`arm112-timer${elapsed >= timerAlertSeconds && !terminal ? " is-alert" : ""}`} role="timer" aria-label="Время обработки">
        <strong>{String(Math.floor(elapsed / 60)).padStart(2, "0")}:{String(elapsed % 60).padStart(2, "0")}</strong>
        <span><span>минут</span><span>секунд</span></span>
      </div>
    </header>
    {item.interruptions.length > 0 && <p role="alert" className="notice">Состояние карточки восстановлено после перезапуска.</p>}
    {isCall && !terminal && (item.state === "offered" || ringingCall) ? <>
      <div className="arm112-ring-row">{outcomeButtons}</div>
      {!callWindowHidden && <IncomingCallDialog aon={item.card.aon} busy={!!pending || accepting} onAccept={accept}
        onDismiss={() => { setCallWindowHidden(true); if (item.state === "offered") send("open", {}); }} />}
    </> : !(opened || terminal) ? <div className="arm112-incoming" role="dialog" aria-label="Новая карточка">
      <strong>Карточка {armCardNumber(item.card.number)}</strong><p>Откройте кейс, чтобы выбрать тип происшествия.</p>
      <button type="button" disabled={!!pending} onClick={() => send("open", {})}>Открыть кейс</button>
    </div> : <>
      <form id="profile-case-form" className="arm112-body" onSubmit={save}>
        <div className="arm112-strip arm112-applicant">
          <input className={`arm112-plain${invalid.has("applicant_name") ? " is-invalid" : ""}`} aria-invalid={invalid.has("applicant_name") || undefined}
            aria-label="Фамилия и имя заявителя" placeholder="Фамилия и имя заявителя" maxLength={1000}
            value={knownValue(draft.applicant_name)} disabled={!editable} onChange={(event) => update("applicant_name", fromText(event.target.value))} />
          <select className="arm112-plain" aria-label="Статус заявителя" value={statusValue} disabled={!editable}
            onChange={(event) => update("applicant_status", event.target.value === "unknown" ? { state: "unknown" } : fromText(event.target.value))}>
            <option value="">выберите статус</option>
            {applicantStatuses.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
            {statusValue && statusValue !== "unknown" && !applicantStatuses.some(([value]) => value === statusValue) && <option value={statusValue}>{statusValue}</option>}
            <option value="unknown">не знает</option>
          </select>
          <button type="button" className="arm112-square" aria-label="Вызов на иностранном языке" title="Вызов на иностранном языке"
            aria-pressed={draft.foreign_language?.state === "known"} disabled={!editable}
            onClick={() => update("foreign_language", draft.foreign_language?.state === "known" ? empty : { state: "known", value: "yes" })}><TranslateIcon size={20} /></button>
        </div>
        <div className="arm112-strip arm112-flags">
          <div className="arm112-victims"><span>Пострадавшие:</span>
            <button type="button" aria-pressed={draft.victims_present.state === "negative"} disabled={!editable}
              onClick={() => setDraft((current) => ({ ...current, victims_present: { state: "negative" }, victims_count: empty }))}>Нет</button>
            <button type="button" aria-pressed={draft.victims_present.state === "known"} disabled={!editable}
              onClick={() => update("victims_present", { state: "known", value: "yes" })}>Есть</button>
            {draft.victims_present.state === "known" && <label>Количество: <input aria-label="Число пострадавших" inputMode="numeric" maxLength={4}
              value={knownValue(draft.victims_count)} disabled={!editable} onChange={(event) => update("victims_count", fromText(event.target.value.replace(/\D/g, "")))} /></label>}
          </div>
          <button type="button" aria-pressed={draft.no_on_site?.state === "known"} disabled={!editable} onClick={() => toggleFlag("no_on_site")}>Нет на месте/<br />Отказ от скорой</button>
          <button type="button" aria-pressed={draft.no_access?.state === "known"} disabled={!editable} onClick={() => toggleFlag("no_access")}>Нет доступа/<br />Заблокированные</button>
        </div>
        {outcomeButtons}

        <div className="arm112-left">
          <section className="arm112-panel arm112-address" aria-label="Адрес">
            <div className="arm112-address-head"><span>Адрес:</span><MapIcon size={17} /></div>
            <div className="arm112-address-line"><output aria-label="Адрес целиком">{addressSummary}</output>
              <button type="button" className="arm112-x" aria-label="Очистить адрес целиком" disabled={!editable} onClick={clearAddress}><CloseIcon size={18} /></button></div>
            <div className="arm112-address-row arm112-cols-3">
              <ArmField label="Страна" field={address.country} invalid={invalid.has("address.country")} disabled={!editable} onChange={(v) => updateAddress("country", v)} />
              <ArmField label="Субъект" field={address.region} invalid={invalid.has("address.region")} disabled={!editable} onChange={(v) => updateAddress("region", v)} />
              <ArmField label="Населённый пункт" field={address.city} invalid={invalid.has("address.city")} disabled={!editable} onChange={(v) => updateAddress("city", v)} />
            </div>
            <div className="arm112-address-row arm112-cols-wide">
              <ArmField label="Объект" field={address.object} invalid={invalid.has("address.object")} disabled={!editable} onChange={(v) => updateAddress("object", v)} />
              <ArmField label="Округ" field={address.okrug} invalid={invalid.has("address.okrug")} disabled={!editable} onChange={(v) => updateAddress("okrug", v)} />
              <ArmField label="Район" field={address.district} invalid={invalid.has("address.district")} disabled={!editable} onChange={(v) => updateAddress("district", v)} />
            </div>
            <div className="arm112-address-row arm112-cols-wide">
              <ArmField label="Улица" field={address.street} invalid={invalid.has("address.street")} disabled={!editable} onChange={(v) => updateAddress("street", v)} />
              <ArmField label="Дом/Вл" field={address.house} invalid={invalid.has("address.house")} disabled={!editable} onChange={(v) => updateAddress("house", v)} />
              <ArmField label="Корпус" field={address.building} invalid={invalid.has("address.building")} disabled={!editable} onChange={(v) => updateAddress("building", v)} />
            </div>
            <div className="arm112-address-row arm112-cols-5">
              <ArmField label="Стр/соор" field={address.structure} invalid={invalid.has("address.structure")} disabled={!editable} onChange={(v) => updateAddress("structure", v)} />
              <ArmField label="Квартира/офис" field={address.flat} invalid={invalid.has("address.flat")} disabled={!editable} onChange={(v) => updateAddress("flat", v)} />
              <ArmField label="Подъезд" field={address.entrance} invalid={invalid.has("address.entrance")} disabled={!editable} onChange={(v) => updateAddress("entrance", v)} />
              <ArmField label="Этаж" field={address.floor} invalid={invalid.has("address.floor")} disabled={!editable} onChange={(v) => updateAddress("floor", v)} />
              <ArmField label="Код" field={address.code} invalid={invalid.has("address.code")} disabled={!editable} onChange={(v) => updateAddress("code", v)} />
            </div>
            <div className="arm112-address-row arm112-cols-descriptive">
              <ArmField label="Описательный адрес" field={address.descriptive} invalid={invalid.has("address.descriptive")} disabled={!editable} onChange={(v) => updateAddress("descriptive", v)} />
              <ArmField label="Ориентир" field={address.landmark} invalid={invalid.has("address.landmark")} disabled={!editable} onChange={(v) => updateAddress("landmark", v)} />
              <button type="button" className="arm112-small" disabled={!editable} onClick={clearAddress}>очистить адрес</button>
            </div>
          </section>
          <section className={`arm112-panel arm112-description${invalid.has("complaint") ? " is-invalid" : ""}`}>
            <label><span>Описание со слов заявителя</span>
              <textarea aria-label="Описание со слов заявителя" placeholder="введите" maxLength={1999} disabled={!editable || draft.complaint.state === "unknown"}
                value={knownValue(draft.complaint)} onChange={(event) => update("complaint", fromText(event.target.value))} /></label>
            <span className="arm112-counter">{knownValue(draft.complaint).length} / 1999</span>
          </section>
          {isCall && state.caller_mode !== "free_text" && <section className="arm112-panel">
            <h3>Разговор с заявителем</h3>
            <div className="intake-transcript"><ol>{state.transcript.map((line, index) => <li key={line.id ?? index}><strong>{line.speaker === "operator" ? "Оператор" : "Заявитель"}:</strong> {line.text}</li>)}</ol></div>
            {state.call_status === "held" && <p>Вызов на удержании. Вернитесь к разговору, чтобы задать вопрос.</p>}
            {state.call_status === "connected" && !terminal && (item.available_questions?.length ?? 0) > 0 && <div className="intake-questions"><h4>Уточняющие вопросы</h4>
              {item.available_questions?.map((question) => <button key={question.id} type="button" disabled={!!pending} onClick={() => send("ask_intake_question", { question_id: question.id })}>{question.text}{question.asked ? " · повторить" : ""}</button>)}
            </div>}
          </section>}
        </div>

        <div className="arm112-right">
          <IncidentTypePicker catalog={catalog} selected={draft.incident_types ?? []} disabled={!editable || !!pending} dirty={dirty}
            onAdd={(id) => send("add_incident_type", { type_id: id })} onRemove={removeType} />
          {catalog?.profiles.filter((profile) => !!draft.profiles?.[profile.id]).map((profile) => {
            const owner = (draft.incident_types ?? []).find((id) => catalog.types.find((type) => type.id === id)?.profile_ids.includes(profile.id));
            return <section className="arm112-profile intake-profile-panel" key={profile.id} id={`arm112-profile-${profile.id}`}>
              <header><h3 title={profile.name}>{profileTitle(profile.name)}</h3>
                {owner && <button type="button" className="arm112-x" aria-label={`Убрать ${profileTitle(profile.name)}`} disabled={!editable || dirty || !!pending} onClick={() => removeType(owner)}><CloseIcon size={18} /></button>}</header>
              {profile.fields.filter((field) => profileFieldVisible(field, draft.profiles![profile.id].answers)).map((field) => <div className="arm112-profile-row intake-profile-row" key={field.id}><span>{field.label}</span>
                {field.kind === "shared" ? <div className="arm112-options"><button type="button" aria-pressed={draft[field.shared!]?.state === "known"} disabled={!editable} onClick={() => toggleFlag(field.shared!)}>
                  {field.shared === "no_on_site" ? "Пострадавший не на месте / Отказ от Скорой" : "Нет доступа"}</button></div>
                  : <ProfileAnswerField label={field.label} kind={field.kind} options={field.options ?? []} answer={draft.profiles![profile.id].answers[field.id]}
                    disabled={!editable} onChange={(answer) => updateAnswer(profile.id, field.id, answer)} />}
              </div>)}
            </section>;
          })}
        </div>
      </form>

      <div className="arm112-feedback" aria-live="polite">
        {dirty && <p>Есть несохранённые изменения — нажмите «сохранить».</p>}
        {!dirty && state.has_saved_draft && !reviewed && !terminal && <p>Черновик сохранён. Проверьте службы (кнопка «+») и зафиксируйте выбор.</p>}
        {!dirty && !notifyFlow && state.service_review && !terminal && <p>Выбор служб зафиксирован: {state.service_review.selected.map((code) => serviceNames[code] ?? code).join(", ") || "службы не выбраны"}.</p>}
        {!dirty && notifyFlow && item.notification && !terminal && <p>Службы оповещены · {formatDateTime(item.notification.notified_at)}: {item.notification.services.map((entry) => serviceNames[entry.service_code] ?? entry.service_code).join(", ") || "службы не выбраны"}.</p>}
        {terminal && <p>{item.state === "interrupted" ? "Занятие остановлено." : item.close_reason === "no_contact" ? "Карточка закрыта: нет контакта с заявителем." : item.close_reason === "call_dropped" ? "Карточка закрыта: срыв звонка." : "Кейс завершён. Результат появится после оценки преподавателя."}</p>}
        {!storage && <p role="alert" className="error">Локальное хранилище недоступно: автоматический повтор команды после сбоя не гарантируется.</p>}
        {pending && <p className="notice">Действие сохраняется…</p>}
        {pending && error && <button type="button" onClick={() => void deliver(pending)}>Повторить отправку</button>}
        {error && <p role="alert" className="error">{errorMessage(error)}</p>}
        {invalid.size > 0 && <p role="alert" className="error">{invalidMessage}</p>}
        {rejection && <p role="alert" className="error">{rejection}</p>}
        {receipt?.outcome === "applied" && <p role="status">Действие сохранено{receipt.replayed ? " после восстановления" : ""}.</p>}
      </div>
    </>}

    <footer className="arm112-bar">
      <span className="arm112-bar-label">Службы:{notified && item.notification ? ` оповещено · ${formatDateTime(item.notification.notified_at)}` : ""}</span>
      <ul className="arm112-services" aria-label="Службы на вызов">
        {barServices.map((code) => {
          const entry = suggested.find((candidate) => candidate.service_code === code);
          return <li key={code} className={`arm112-service-tile${reviewed ? "" : " is-proposed"}${entry ? " is-main" : ""}`} title={entry ? `${serviceNames[code] ?? code}: ${entry.reasons.join("; ")}` : serviceNames[code] ?? code}>
            <PhoneIcon size={14} /><strong>{serviceTiles[code] ?? code}</strong>
            <button type="button" className="arm112-tile-x" aria-label={`Убрать службу ${serviceTiles[code] ?? code}`} disabled={reviewBlocked}
              onClick={() => { setSelectedServices((current) => current.filter((candidate) => candidate !== code)); setServicesOpen(true); }}><CloseIcon size={11} /></button>
          </li>;
        })}
      </ul>
      <button type="button" className="arm112-bar-square" aria-label="Добавить службу" disabled={reviewBlocked} title={reviewBlocked && editable ? "Сохраните карточку, чтобы выбрать службы" : undefined}
        onClick={() => setServicesOpen(true)}><PlusIcon size={26} /></button>
      <div className="arm112-bar-actions">
        {(invalid.size > 0 || rejection) && <p className="arm112-bar-error" aria-hidden="true">{invalid.size > 0 ? invalidMessage : rejection}</p>}
        {!terminal && editable && <button type="submit" form="profile-case-form" className="arm112-bar-text" aria-label="Сохранить карточку" disabled={!!pending || !dirty && state.has_saved_draft}>сохранить</button>}
        {opened && <button type="button" className="arm112-bar-text" aria-label="Завершить кейс" disabled={!!pending || dirty || !state.has_saved_draft || !(notifyFlow ? notified : state.service_review) || !(draft.incident_types?.length) || (isCall && state.call_status !== "ended")}
          title="Доступно после сохранения карточки и фиксации служб" onClick={() => send(notifyFlow ? "complete_intake" : "complete_profile_case", {})}>завершить</button>}
        <button type="button" className="arm112-bar-square" aria-label="Связать карточки" title={unavailable} disabled><LinkIcon size={24} /></button>
        <button type="button" className="arm112-bar-square" aria-label="Напоминание" title={unavailable} disabled><StopwatchIcon size={24} /></button>
        <button type="button" className="arm112-bar-square" aria-label="Важное происшествие" title={unavailable} disabled><BellIcon size={24} /></button>
        <button type="button" className="arm112-bar-square" aria-label="Сообщение об ошибке" title={unavailable} disabled><MessageIcon size={24} /></button>
        {onClose && <button type="button" className="arm112-bar-square" aria-label="К списку вызовов" title="Закрыть карточку и вернуться к списку" onClick={onClose}><CloseIcon size={24} /></button>}
      </div>
    </footer>

    {servicesOpen && <div className="arm112-modal-backdrop" onClick={(event) => { if (event.target === event.currentTarget) { resetSelection(); setServicesOpen(false); } }}>
      <div className="arm112-modal" role="dialog" aria-modal="true" aria-label={notifyFlow ? "Список оповещаемых служб" : "Добавьте службы"}>
        <button type="button" className="arm112-modal-close" aria-label="Закрыть без изменений" onClick={() => { resetSelection(); setServicesOpen(false); }}><CloseIcon size={18} /></button>
        <h2>{notifyFlow ? "Список оповещаемых служб" : "Добавьте службы"}</h2>
        {suggested.length > 0 ? <p className="arm112-modal-hint">Предложено по типам и признакам: {suggested.map((entry) => `${serviceTiles[entry.service_code] ?? entry.service_code} (${entry.reasons.join("; ")})`).join(", ")}.</p>
          : <p className="arm112-modal-hint">Система пока не предложила служб.</p>}
        <ul className="arm112-modal-list">
          {allServices.map((code) => <li key={code}><button type="button" aria-pressed={selectedServices.includes(code)}
            onClick={() => setSelectedServices((current) => current.includes(code) ? current.filter((entry) => entry !== code) : [...current, code])}>{serviceNames[code] ?? code}</button></li>)}
        </ul>
        {selectionChanged && <label className="arm112-modal-reason">Причина изменения предложения
          <input aria-label="Причина изменения предложения" value={reviewReason} maxLength={1000} onChange={(event) => setReviewReason(event.target.value)} /></label>}
        <div className="arm112-modal-actions">
          {notifyFlow && <button type="button" className="arm112-modal-cancel" onClick={() => { resetSelection(); setServicesOpen(false); }}>вернуться к заполнению</button>}
          <button type="button" className="arm112-modal-save" disabled={reviewBlocked || (selectionChanged && !reviewReason.trim()) || (notifyFlow && selectedServices.length === 0)}
            onClick={() => { send(notifyFlow ? "notify_services" : "review_service_selection", { services: selectedServices, reason: reviewReason.trim() }); setServicesOpen(false); }}>
            {notifyFlow ? "оповестить и сохранить карточку" : "Сохранить и закрыть"}
          </button>
        </div>
      </div>
    </div>}

    {outcomeIntent && <div className="intake-outcome-backdrop"><div className="intake-outcome-dialog" role="dialog" aria-modal="true" aria-label="Завершение вызова">
      <h3>{outcomeIntent === "no_contact" ? "Нет контакта с заявителем" : "Срыв звонка"}</h3>
      <p>Карточка закроется без направления в службы. Действие сохранится в журнале.</p>
      <div><button type="button" onClick={() => setOutcomeIntent(null)}>Вернуться к карточке</button>
        <button type="button" className="arm-primary-action" disabled={!!pending} onClick={() => { send(outcomeIntent === "no_contact" ? "mark_no_contact" : "mark_call_dropped", {}); setOutcomeIntent(null); }}>Закрыть карточку</button></div>
    </div></div>}

    {isCall && state.caller_mode === "free_text" && bodyReady && <CallerChat item={item} open={chatOpen} onToggle={toggleChat} pending={!!pending} rejected={chatRejection}
      onSend={(text, input) => send("send_caller_message", input ? { text, input } : { text })} />}
  </section>;
}

function IncidentTypePicker({ catalog, selected, disabled, dirty, onAdd, onRemove }: {
  catalog?: IntakeCatalog; selected: string[]; disabled: boolean; dirty: boolean; onAdd: (id: string) => void; onRemove: (id: string) => void;
}) {
  const [query, setQuery] = useState("");
  const [open, setOpen] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  const types = catalog?.types ?? [];
  const available = types.filter((type) => !selected.includes(type.id));
  const matches = available.filter((type) => type.name.toLowerCase().includes(query.trim().toLowerCase()));
  const blocked = disabled || dirty;
  const choose = (id: string) => { if (blocked) return; onAdd(id); setQuery(""); setOpen(false); };
  useEffect(() => {
    const close = (event: MouseEvent) => { if (!box.current?.contains(event.target as Node)) setOpen(false); };
    document.addEventListener("mousedown", close);
    return () => document.removeEventListener("mousedown", close);
  }, []);
  return <>
    <section className="arm112-panel arm112-type" ref={box}>
      <div className="arm112-type-search">
        <input role="combobox" aria-label="Тип происшествия" aria-expanded={open} aria-controls="arm112-type-options" aria-autocomplete="list"
          placeholder="добавить тип происшествия" value={query} disabled={disabled}
          onFocus={() => setOpen(true)} onChange={(event) => { setQuery(event.target.value); setOpen(true); }}
          onKeyDown={(event) => {
            if (event.key === "Escape") setOpen(false);
            if (event.key === "Enter") { event.preventDefault(); if (matches[0]) choose(matches[0].id); }
          }} />
        {query && <button type="button" className="arm112-x" aria-label="Очистить поиск типа" onClick={() => setQuery("")}><CloseIcon size={18} /></button>}
        {open && !disabled && <ul id="arm112-type-options" role="listbox" className="arm112-type-options">
          {dirty && <li className="arm112-type-hint">Сохраните карточку, чтобы добавить тип</li>}
          {matches.map((type) => <li key={type.id} role="option" aria-selected={false} aria-disabled={blocked}
            onMouseDown={(event) => event.preventDefault()} onClick={() => choose(type.id)}>{type.name}</li>)}
          {!matches.length && <li className="arm112-type-hint">Ничего не найдено</li>}
        </ul>}
      </div>
      {selected.length ? <div className="arm112-chosen-types">
        {selected.map((id) => <span key={id} className="arm112-chosen-type">{types.find((type) => type.id === id)?.name ?? id}
          <button type="button" aria-label={`Убрать тип ${types.find((type) => type.id === id)?.name ?? id}`} disabled={blocked} onClick={() => onRemove(id)}><CloseIcon size={12} /></button></span>)}
      </div> : <div className="arm112-quick-types">
        {available.map((type) => <button type="button" key={type.id} disabled={blocked} onClick={() => choose(type.id)}>{type.name}</button>)}
      </div>}
      {dirty && !disabled && <p className="arm112-type-note">Сохраните изменения карточки перед изменением типа.</p>}
    </section>
    {selected.length > 0 && catalog && catalog.profiles.length > 0 && <nav className="arm112-panel arm112-profile-tabs" aria-label="Карты происшествия">
      {catalog.profiles.map((profile) => <a key={profile.id} href={`#arm112-profile-${profile.id}`}>{profileTitle(profile.name)}</a>)}
    </nav>}
  </>;
}

export function ProfileAnswerField({ label, kind, options, answer, disabled, onChange }: {
  label: string; kind: "single" | "multiple" | "text"; options: string[]; answer: IntakeProfileAnswer; disabled: boolean; onChange: (answer: IntakeProfileAnswer) => void;
}) {
  const unknownButton = <button type="button" className="arm112-option-unknown" aria-pressed={answer.state === "unknown"} disabled={disabled}
    onClick={() => onChange(answer.state === "unknown" ? { state: "unanswered" } : { state: "unknown" })}>Неизвестно</button>;
  if (kind === "text") return <div className="arm112-options intake-profile-options arm112-options-text"><textarea aria-label={`${label}: значение`} disabled={disabled || answer.state === "unknown"} value={answer.state === "known" ? answer.value ?? "" : ""} maxLength={1999}
    onChange={(event) => onChange(event.target.value ? { state: "known", value: event.target.value } : { state: "unanswered" })} />{unknownButton}</div>;
  return <div className="arm112-options intake-profile-options">{options.map((option) => {
    const selected = kind === "single" ? answer.state === "known" && answer.value === option : answer.state === "known" && (answer.values ?? []).includes(option);
    return <button type="button" key={option} aria-pressed={selected} disabled={disabled} onClick={() => {
      if (kind === "single") onChange(selected ? { state: "unanswered" } : { state: "known", value: option });
      else { const values = selected ? (answer.values ?? []).filter((value) => value !== option) : [...(answer.state === "known" ? answer.values ?? [] : []), option]; onChange(values.length ? { state: "known", values } : { state: "unanswered" }); }
    }}>{option}</button>;
  })}{unknownButton}</div>;
}
