import { useEffect, useState, type FormEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { executeCommand, type Command, type Receipt } from "../../api/commands";
import { errorMessage } from "../../api/errors";
import type { Me } from "../../api/useMe";
import { itemQueryKey, myItemsQueryKey, myRunQueryKey, type Item } from "../../api/workplace";
import { availableLocalStorage, clearPending, loadPending, savePending, type PendingCommand } from "../../commands/pending";
import { formatDateTime } from "../../format";
import { Operator112ProfileCase } from "./Operator112ProfileCase";

export type IntakeField = { state: "unanswered" | "known" | "unknown" | "negative"; value?: string };
export type IntakeCard = {
  number: string; aon: string; call_local_time: string; call_time_zone: string;
  applicant_name: IntakeField; applicant_status: IntakeField; age: IntakeField;
  address: {
    country: IntakeField; region: IntakeField; city: IntakeField; object: IntakeField; okrug: IntakeField; district: IntakeField;
    street: IntakeField; house: IntakeField; building: IntakeField; structure: IntakeField; flat: IntakeField;
    entrance: IntakeField; floor: IntakeField; code: IntakeField; landmark: IntakeField; descriptive: IntakeField;
  };
  incident_type: IntakeField; complaint: IntakeField; victims_present: IntakeField; victims_count: IntakeField; provided_phone: IntakeField;
  on_site_phone: IntakeField; channel: IntakeField; foreign_language: IntakeField; no_on_site: IntakeField; no_access: IntakeField;
  incident_types?: string[]; profiles?: Record<string, IntakeProfileAnswerSet>;
};
export type IntakeProfileAnswer = { state: "unanswered" | "unknown" | "known"; value?: string; values?: string[] };
export type IntakeProfileAnswerSet = { definition_id: string; version: number; answers: Record<string, IntakeProfileAnswer> };
export type IntakeCatalog = { version: number; types: { id: string; name: string; profile_ids: string[] }[];
  profiles: { id: string; version: number; name: string; fields: IntakeProfileFieldDef[] }[];
  service_rules: { id: string; profile_id: string; field_id?: string; equals?: string; service_code: string; reason: string }[] };
export type IntakeProfileFieldDef = { id: string; label: string; kind: "single" | "multiple" | "text" | "shared"; options?: string[];
  shared?: "no_on_site" | "no_access"; visible_when?: { field_id: string; any_of: string[] } };
export type IntakeLine = { id?: string; speaker?: "caller" | "operator"; text: string; server_at: string; topic_id?: string };
// 112-5a/ADR-024: one send_caller_message/caller.reply round trip. status
// stays "pending" until the async worker answers (or hold/end/close
// cancels it, or the finalizer fails it after exhausted retries).
export type IntakeCallerTurn = { turn: number; operator_line_id?: string; status: "pending" | "answered" | "cancelled" | "failed";
  adapter?: string; requested_at: string; resolved_at?: string; reason?: string };
export type IntakeState = { mode?: "card_only" | "full_case"; catalog?: IntakeCatalog; call_status: "ringing" | "connected" | "held" | "ended" | "not_applicable"; transcript: IntakeLine[]; asked_question_ids?: string[];
  caller_mode?: "prepared" | "free_text"; caller_turns?: IntakeCallerTurn[];
  suggested_services?: { service_code: string; reasons: string[] }[]; service_review?: { selected: string[]; reason?: string; reviewed_at: string };
  has_saved_draft: boolean; dispatched: boolean; selected_service?: string; answered_at?: string; ended_at?: string; finale?: string; notified: boolean };
type Dispatch = { service_code: string; sent_at: string; card_snapshot: IntakeCard };
export type IntakeNotificationService = { service_code: string; suggested: boolean };
export type IntakeNotification = { item_id: string; action_id: string; services: IntakeNotificationService[]; reason?: string; card_snapshot: IntakeCard; notified_at: string };
type IntakeQuestion = { id: string; text: string; topic_id: string; asked: boolean };
export type IntakeItem = Omit<Item, "card"> & { card: IntakeCard; intake_state: IntakeState; available_questions?: IntakeQuestion[];
  available_service_codes?: string[]; recipient_services: string[]; dispatch?: Dispatch; notification?: IntakeNotification };

const addressKeys = ["country", "region", "city", "object", "okrug", "district", "street", "house", "building", "structure", "flat", "entrance", "floor", "code", "landmark", "descriptive"] as const;
const unanswered: IntakeField = { state: "unanswered" };
const legacyChannelLabel = "Телефон, оператор не указан";
const channelSuggestions = ["МТС", "Мегафон", "Билайн", "Теле2", "Мобильное приложение", "Стационарный телефон", legacyChannelLabel];

function savedAvailability(storage: Storage | null, key: string): "available" | "unavailable" {
  try { return storage?.getItem(key) === "unavailable" ? "unavailable" : "available"; }
  catch { return "available"; }
}

// Rows created before the expanded form have no values for its new fields.
function completeCard(card: IntakeCard): IntakeCard {
  const address = { ...card.address };
  for (const key of addressKeys) address[key] ??= unanswered;
  return { ...card, address, on_site_phone: card.on_site_phone ?? unanswered,
    channel: card.channel ?? unanswered, foreign_language: card.foreign_language ?? unanswered,
    no_on_site: card.no_on_site ?? unanswered, no_access: card.no_access ?? unanswered };
}

const errorLabels: Record<string, string> = {
  stale_seq: "Карточка изменилась. Проверьте новые данные и повторите действие.",
  transition_not_allowed: "Это действие сейчас недоступно.",
  invalid_payload: "Проверьте поля карточки.",
  item_closed: "Обработка уже завершена.",
  lesson_stopped: "Занятие остановлено преподавателем.",
};

export function Operator112Workplace({ me, item, onClose }: { me: Me; item: IntakeItem; onClose?: () => void }) {
  return item.intake_state.mode === "card_only" || item.intake_state.mode === "full_case"
    ? <Operator112ProfileCase me={me} item={item} onClose={onClose} /> : <Operator112IncomingWorkplace me={me} item={item} />;
}

function Operator112IncomingWorkplace({ me, item }: { me: Me; item: IntakeItem }) {
  const client = useQueryClient();
  const [storage] = useState(() => availableLocalStorage());
  const [pending, setPending] = useState<PendingCommand | null>(() => storage ? loadPending(storage, me.user.id, item.id) : null);
  const [receipt, setReceipt] = useState<Receipt | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [outcomeIntent, setOutcomeIntent] = useState<"no_contact" | "call_dropped" | null>(null);
  const [draft, setDraft] = useState<IntakeCard>(() => completeCard(item.card));
  const [clientNow, setClientNow] = useState(Date.now);
  const [clock] = useState(() => ({ client: Date.now(), server: new Date(item.server_time).getTime() }));
  const availabilityKey = `emsim:112:availability:${me.user.id}:${me.workstation?.number ?? "unknown"}`;
  const [manualAvailability, setManualAvailability] = useState(() => savedAvailability(storage, availabilityKey));
  const state = item.intake_state;
  const terminal = item.state === "closed" || item.state === "interrupted";
  const dirty = JSON.stringify(draft) !== JSON.stringify(completeCard(item.card));
  const serverNow = clock.server + clientNow - clock.client;
  const elapsed = Math.max(0, Math.floor((serverNow - new Date(item.offered_at).getTime()) / 1000));
  const availabilityForced = item.state === "opened" || item.state === "in_progress" || (terminal && !!item.closed_at && serverNow < new Date(item.closed_at).getTime() + 10_000);
  const availability = availabilityForced ? "unavailable" : manualAvailability;

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
      await refresh();
      if (storage) clearPending(storage, me.user.id, value.item_id, value.command.command_id);
      setPending(null);
    } catch (cause) { setError(cause); }
  };
  useEffect(() => { if (pending) queueMicrotask(() => { void deliver(pending); }); /* restore exactly the stored command once on mount */ }, []); // eslint-disable-line react-hooks/exhaustive-deps
  const send = (type: Command["type"], payload: Record<string, unknown>) => {
    if (pending) return;
    const command: Command = { command_id: crypto.randomUUID(), expected_seq: item.seq, type, payload, client_at: new Date().toISOString() };
    const value = { item_id: item.id, command };
    try {
      if (storage) savePending(storage, me.user.id, item.id, command);
      setPending(value);
      void deliver(value);
    } catch (cause) { setError(cause); }
  };
  const update = (key: keyof IntakeCard, field: IntakeField) => setDraft((current) => ({ ...current, [key]: field }));
  const updateAddress = (key: keyof IntakeCard["address"], field: IntakeField) => setDraft((current) => ({ ...current, address: { ...current.address, [key]: field } }));
  const save = (event: FormEvent) => { event.preventDefault(); send("save_intake_draft", { draft }); };
  const rejection = receipt?.outcome === "rejected" ? errorLabels[receipt.error_code ?? ""] ?? receipt.error_code : null;
  const locked = terminal || state.dispatched;
  const serviceCode = "pilot_ambulance";
  const addressSummary = [draft.address.city, draft.address.street, draft.address.house, draft.address.building]
    .filter((field) => field.state === "known").map((field) => field.value).join(", ");
  const clearAddress = () => setDraft((current) => ({ ...current, address: Object.fromEntries(addressKeys.map((key) => [key, unanswered])) as IntakeCard["address"] }));
  const toggleFlag = (key: "no_on_site" | "no_access") => {
    setDraft((current) => ({ ...current, [key]: current[key].state === "known" ? unanswered : { state: "known", value: "yes" } }));
  };
  const toggleAvailability = () => {
    if (availabilityForced) return;
    const next = manualAvailability === "available" ? "unavailable" : "available";
    setManualAvailability(next);
    try { storage?.setItem(availabilityKey, next); } catch { /* local indication still works for this mount */ }
  };
  const channelField = draft.channel.state === "known" && draft.channel.value === "phone"
    ? { state: "known" as const, value: legacyChannelLabel } : draft.channel;

  return <section className="intake-workplace">
    <header className="intake-console">
      <div className="intake-console-icon" aria-hidden="true">☎</div>
      <section className="intake-console-call" aria-label="Входящий вызов">
        <button type="button" className="intake-availability" aria-label="Статус телефонии" aria-pressed={availability === "available"} disabled={availabilityForced} onClick={toggleAvailability}>{availability === "available" ? "Доступен" : "Недоступен"}</button>
        <span className="intake-call-state">{state.call_status === "ringing" ? "Входящий вызов" : state.call_status === "connected" ? "Разговор" : state.call_status === "held" ? "На удержании" : "Разговор завершён"}</span>
        <div className="intake-call-actions">
          {item.state === "offered" && <button type="button" disabled={!!pending} onClick={() => send("open", {})}>Открыть вызов</button>}
          {item.state === "opened" && state.call_status === "ringing" && <button type="button" disabled={!!pending} onClick={() => send("answer_incoming", {})}>Ответить</button>}
          {state.call_status === "connected" && !terminal && <button type="button" disabled={!!pending} onClick={() => send("hold_incoming", {})}>Удержать</button>}
          {state.call_status === "held" && !terminal && <button type="button" disabled={!!pending} onClick={() => send("resume_incoming", {})}>Вернуться к разговору</button>}
          {(state.call_status === "connected" || state.call_status === "held") && !terminal && <button type="button" disabled={!!pending} onClick={() => send("end_incoming", {})}>Завершить разговор</button>}
        </div>
      </section>
      <div className="intake-console-number"><span>АОН</span><strong>{item.card.aon}</strong></div>
      <div className="intake-console-number">{state.call_status === "ringing" ? <><span>Предоставленный</span><strong>—</strong></> : <>
        <Field label="Телефон со слов заявителя" field={draft.provided_phone} disabled={locked} onChange={(v) => update("provided_phone", v)} />
        <button type="button" disabled={locked} onClick={() => update("provided_phone", { state: "known", value: item.card.aon })}>АОН</button>
      </>}</div>
      <div className="intake-console-number">{state.call_status === "ringing" ? <><span>Телефон на место</span><strong>—</strong></> : <>
        <Field label="Телефон на место" field={draft.on_site_phone} disabled={locked} onChange={(v) => update("on_site_phone", v)} />
        <button type="button" disabled={locked} onClick={() => update("on_site_phone", { state: "known", value: item.card.aon })}>АОН</button>
      </>}</div>
      <div className="intake-console-incident"><strong>Происшествие {item.card.number}</strong><span>Вызов {item.card.call_local_time} · {item.card.call_time_zone}</span><span>Оператор: {me.user.full_name}</span></div>
      <div className="intake-elapsed"><strong>{String(Math.floor(elapsed / 60)).padStart(2, "0")}:{String(elapsed % 60).padStart(2, "0")}</strong><span>минуты : секунды</span></div>
      {!terminal && <div className="intake-outcome-controls">
        <button type="button" disabled={!!pending || item.state !== "opened" || state.call_status !== "ringing"} onClick={() => setOutcomeIntent("no_contact")}>Нет контакта</button>
        <button type="button" disabled={!!pending || (state.call_status !== "connected" && state.call_status !== "held") || state.dispatched} onClick={() => setOutcomeIntent("call_dropped")}>Срыв звонка</button>
      </div>}
    </header>
    {item.interruptions.length > 0 && <p role="alert" className="notice">После перезапуска сервера состояние вызова и карточки восстановлено.</p>}
    {state.call_status === "ringing" ? <div className="intake-waiting">Примите вызов, чтобы открыть слова заявителя и карточку.</div> : <>
      <form id="intake-card-form" className="intake-main" onSubmit={save}>
        <div className="intake-left">
          <section className="intake-applicant intake-panel">
            <div className="intake-applicant-row">
              <Field label="ФИО заявителя" field={draft.applicant_name} disabled={locked} onChange={(v) => update("applicant_name", v)} />
              <ApplicantStatusField field={draft.applicant_status} disabled={locked} onChange={(v) => update("applicant_status", v)} />
              <Field label="Канал связи" field={channelField} disabled={locked} suggestions={channelSuggestions} onChange={(v) => update("channel", v.state === "known" && v.value === legacyChannelLabel ? { state: "known", value: "phone" } : v)} />
            </div>
          </section>
          <section className="intake-address intake-panel">
            <h3>Адрес</h3>
            <div className="intake-address-summary">{addressSummary || "Адрес ещё не внесён"}</div>
            <div className="intake-address-grid">
              <Field label="Страна" field={draft.address.country} disabled={locked} onChange={(v) => updateAddress("country", v)} />
              <Field label="Субъект" field={draft.address.region} disabled={locked} onChange={(v) => updateAddress("region", v)} />
              <Field label="Населённый пункт" field={draft.address.city} disabled={locked} onChange={(v) => updateAddress("city", v)} />
              <Field label="Объект" field={draft.address.object} disabled={locked} onChange={(v) => updateAddress("object", v)} />
              <Field label="Округ" field={draft.address.okrug} disabled={locked} onChange={(v) => updateAddress("okrug", v)} />
              <Field label="Район" field={draft.address.district} disabled={locked} onChange={(v) => updateAddress("district", v)} />
              <Field label="Улица" field={draft.address.street} disabled={locked} onChange={(v) => updateAddress("street", v)} />
              <Field label="Дом" field={draft.address.house} disabled={locked} onChange={(v) => updateAddress("house", v)} />
              <Field label="Корпус" field={draft.address.building} disabled={locked} onChange={(v) => updateAddress("building", v)} />
              <Field label="Строение" field={draft.address.structure} disabled={locked} onChange={(v) => updateAddress("structure", v)} />
              <Field label="Квартира / офис" field={draft.address.flat} disabled={locked} onChange={(v) => updateAddress("flat", v)} />
              <Field label="Подъезд" field={draft.address.entrance} disabled={locked} onChange={(v) => updateAddress("entrance", v)} />
              <Field label="Этаж" field={draft.address.floor} disabled={locked} onChange={(v) => updateAddress("floor", v)} />
              <Field label="Код" field={draft.address.code} disabled={locked} onChange={(v) => updateAddress("code", v)} />
              <Field label="Ориентир" field={draft.address.landmark} disabled={locked} onChange={(v) => updateAddress("landmark", v)} />
              <Field label="Описательный адрес" field={draft.address.descriptive} disabled={locked} onChange={(v) => updateAddress("descriptive", v)} />
            </div>
            <button type="button" className="intake-clear-address" disabled={locked} onClick={clearAddress}>Очистить адрес</button>
          </section>
          <section className="intake-description intake-panel">
            <h3>Описание со слов заявителя</h3>
            <Field label="Жалобы" field={draft.complaint} disabled={locked} multiline onChange={(v) => update("complaint", v)} />
            <span className="intake-character-count">{draft.complaint.state === "known" ? draft.complaint.value?.length ?? 0 : 0} / 1999</span>
          </section>
        </div>
        <div className="intake-right">
          <section className="intake-status-strip intake-panel">
            <div className="intake-victims-buttons">
              <button type="button" aria-pressed={draft.victims_present.state === "known"} disabled={locked} onClick={() => update("victims_present", { state: "known", value: "yes" })}>Пострадавшие</button>
              <button type="button" aria-pressed={draft.victims_present.state === "negative"} disabled={locked} onClick={() => update("victims_present", { state: "negative" })}>Нет пострадавших</button>
            </div>
            <button type="button" aria-pressed={draft.no_on_site.state === "known"} disabled={locked} onClick={() => toggleFlag("no_on_site")}>Нет на месте / Отказ от скорой</button>
            <button type="button" aria-pressed={draft.no_access.state === "known"} disabled={locked} onClick={() => toggleFlag("no_access")}>Нет доступа / Заблокированные</button>
          </section>
          <section className="intake-victims intake-panel"><Field label="Число пострадавших" field={draft.victims_count} disabled={locked} numeric onChange={(v) => update("victims_count", v)} /></section>
          <section className="intake-transcript intake-panel"><h3>Разговор с заявителем</h3>
            <ol>{state.transcript.map((line, index) => <li key={line.id ?? index}><strong>{line.speaker === "operator" ? "Оператор" : "Заявитель"}:</strong> {line.text}</li>)}</ol>
            {state.call_status === "held" && <p>Вызов на удержании. Вернитесь к разговору, чтобы задать вопрос.</p>}
            {state.call_status === "connected" && !terminal && (item.available_questions?.length ?? 0) > 0 && <div className="intake-questions"><h4>Уточняющие вопросы</h4>
              {item.available_questions?.map((question) => <button key={question.id} type="button" disabled={!!pending} onClick={() => send("ask_intake_question", { question_id: question.id })}>{question.text}{question.asked ? " · повторить" : ""}</button>)}
            </div>}
          </section>
        </div>
      </form>
      <footer className="intake-action-bar">
        <div className="intake-service"><strong>Службы:</strong><span className="intake-service-tile">03 · Скорая помощь</span></div>
        <div className="intake-actions">
          {!terminal && !state.dispatched && <button type="submit" form="intake-card-form" disabled={!!pending || (state.has_saved_draft && !dirty)}>Сохранить карточку</button>}
          {!item.dispatch && <button type="button" disabled={terminal || !!pending || !state.has_saved_draft || dirty} onClick={() => send("dispatch_intake", { service_code: serviceCode })}>Направить в 03</button>}
          {!terminal && state.call_status === "ended" && state.dispatched && <button type="button" disabled={!!pending} onClick={() => send("complete_intake", {})}>Завершить обработку</button>}
        </div>
      </footer>
      <div className="intake-feedback" aria-live="polite">
        {item.dispatch && <p>Направлена в 03 · {formatDateTime(item.dispatch.sent_at)}. Снимок отправки сохранён.</p>}
        {state.has_saved_draft && !item.dispatch && <p>{dirty ? "Есть изменения после последнего сохранения. Сохраните их перед отправкой." : "Черновик сохранён на сервере."}</p>}
        {terminal && <p>{item.state === "interrupted" ? "Занятие остановлено; карточка закрыта." : item.close_reason === "no_contact" ? "Карточка закрыта: нет контакта с заявителем." : item.close_reason === "call_dropped" ? "Карточка закрыта: срыв звонка." : "Обработка завершена. Результат появится после оценки преподавателя."}</p>}
      </div>
    </>}
    {outcomeIntent && <div className="intake-outcome-backdrop"><div className="intake-outcome-dialog" role="dialog" aria-modal="true" aria-label="Завершение вызова">
      <h3>{outcomeIntent === "no_contact" ? "Нет контакта с заявителем" : "Срыв звонка"}</h3>
      <p>Карточка закроется без направления в службу 03. Действие сохранится в журнале.</p>
      <div><button type="button" onClick={() => setOutcomeIntent(null)}>Вернуться к карточке</button>
        <button type="button" className="arm-primary-action" disabled={!!pending} onClick={() => { send(outcomeIntent === "no_contact" ? "mark_no_contact" : "mark_call_dropped", {}); setOutcomeIntent(null); }}>Закрыть карточку</button></div>
    </div></div>}
    {!storage && <p role="alert" className="error">Локальное хранилище недоступно: автоматический повтор команды после сбоя не гарантируется.</p>}
    {pending && <p className="notice">Действие сохраняется…</p>}
    {pending && error && <button type="button" onClick={() => void deliver(pending)}>Повторить отправку</button>}
    {error && <p role="alert" className="error">{errorMessage(error)}</p>}
    {rejection && <p role="alert" className="error">{rejection}</p>}
    {receipt?.outcome === "applied" && <p role="status">Действие сохранено{receipt.replayed ? " после восстановления" : ""}.</p>}
  </section>;
}

function ApplicantStatusField({ field, disabled, onChange }: { field: IntakeField; disabled: boolean; onChange: (value: IntakeField) => void }) {
  const statuses = [{ value: "victim", label: "Пострадавший" }, { value: "relative", label: "Родственник" },
    { value: "witness", label: "Очевидец" }, { value: "friend", label: "Знакомый" },
    { value: "child", label: "Ребёнок" }, { value: "participant", label: "Участник" }];
  return <div className="intake-field intake-applicant-status">
    <span>Статус заявителя</span>
    <select aria-label="Статус заявителя: значение" disabled={disabled}
      value={field.state === "known" ? field.value ?? "" : field.state === "unknown" ? "unknown" : ""}
      onChange={(event) => onChange(event.target.value === "unknown" ? { state: "unknown" } : event.target.value ? { state: "known", value: event.target.value } : unanswered)}>
      <option value="">Выберите статус</option>
      {statuses.map((status) => <option key={status.value} value={status.value}>{status.label}</option>)}
      <option value="unknown">Не знает</option>
    </select>
  </div>;
}

function Field({ label, field, onChange, disabled, suggestions, numeric, multiline }: {
  label: string; field: IntakeField; onChange: (value: IntakeField) => void; disabled: boolean;
  suggestions?: string[]; numeric?: boolean; multiline?: boolean;
}) {
  const state = field?.state || "unanswered";
  const changeState = (next: IntakeField["state"]) => onChange(next === "known" ? { state: next, value: field.value ?? "" } : { state: next });
  const changeValue = (value: string) => onChange(value ? { state: "known", value } : { state: "unanswered" });
  return <div className="intake-field">
    <span>{label}</span>
    <div className="intake-field-control">
      {multiline ? <textarea aria-label={`${label}: значение`} value={state === "known" ? field.value ?? "" : ""} disabled={disabled} maxLength={1999} placeholder="Введите" onChange={(event) => changeValue(event.target.value)} />
        : <><input aria-label={`${label}: значение`} type={numeric ? "number" : "text"} min={numeric ? 0 : undefined} list={suggestions ? "intake-channel-options" : undefined} value={state === "known" ? field.value ?? "" : ""} disabled={disabled} maxLength={suggestions ? 100 : 1000} placeholder="Введите" onChange={(event) => changeValue(event.target.value)} />{suggestions && <datalist id="intake-channel-options">{suggestions.map((suggestion) => <option key={suggestion} value={suggestion} />)}</datalist>}</>}
      <select aria-label={`${label}: статус`} value={state} disabled={disabled} onChange={(event) => changeState(event.target.value as IntakeField["state"])} title="Не заполнено или неизвестно заявителю">
        <option value="unanswered">—</option><option value="known">Известно</option><option value="unknown">Не знает</option>
      </select>
    </div>
  </div>;
}
