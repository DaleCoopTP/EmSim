import { useEffect, useState, type FormEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { executeCommand, type Command, type Receipt } from "../../api/commands";
import { errorMessage } from "../../api/errors";
import type { Me } from "../../api/useMe";
import { itemQueryKey, myItemsQueryKey, myRunQueryKey, type Item } from "../../api/workplace";
import { availableLocalStorage, clearPending, loadPending, savePending, type PendingCommand } from "../../commands/pending";
import { formatDateTime } from "../../format";

export type IntakeField = { state: "unanswered" | "known" | "unknown" | "negative"; value?: string };
export type IntakeCard = {
  number: string; aon: string; call_local_time: string; call_time_zone: string;
  applicant_name: IntakeField; applicant_status: IntakeField; age: IntakeField;
  address: { city: IntakeField; street: IntakeField; house: IntakeField; building: IntakeField; flat: IntakeField; landmark: IntakeField };
  incident_type: IntakeField; complaint: IntakeField; victims_present: IntakeField; victims_count: IntakeField; provided_phone: IntakeField;
};
type IntakeState = { call_status: "ringing" | "connected" | "ended"; transcript: Array<{ text: string; server_at: string }>;
  has_saved_draft: boolean; dispatched: boolean; selected_service?: string; answered_at?: string; ended_at?: string };
type Dispatch = { service_code: string; sent_at: string; card_snapshot: IntakeCard };
export type IntakeItem = Omit<Item, "card"> & { card: IntakeCard; intake_state: IntakeState; recipient_services: string[]; dispatch?: Dispatch };

const errorLabels: Record<string, string> = {
  stale_seq: "Карточка изменилась. Проверьте новые данные и повторите действие.",
  transition_not_allowed: "Это действие сейчас недоступно.",
  invalid_payload: "Проверьте поля карточки.",
  item_closed: "Обработка уже завершена.",
  lesson_stopped: "Занятие остановлено преподавателем.",
};

export function Operator112Workplace({ me, item }: { me: Me; item: IntakeItem }) {
  const client = useQueryClient();
  const [storage] = useState(() => availableLocalStorage());
  const [pending, setPending] = useState<PendingCommand | null>(() => storage ? loadPending(storage, me.user.id, item.id) : null);
  const [receipt, setReceipt] = useState<Receipt | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [draft, setDraft] = useState<IntakeCard>(() => item.card);
  const [serviceCode, setServiceCode] = useState(item.recipient_services?.[0] ?? "");
  const [clientNow, setClientNow] = useState(Date.now);
  const [clock] = useState(() => ({ client: Date.now(), server: new Date(item.server_time).getTime() }));
  const state = item.intake_state;
  const terminal = item.state === "closed" || item.state === "interrupted";
  const dirty = JSON.stringify(draft) !== JSON.stringify(item.card);
  const elapsed = Math.max(0, Math.floor((clock.server + clientNow - clock.client - new Date(item.offered_at).getTime()) / 1000));

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

  return <section className="intake-workplace">
    <header className="intake-header">
      <div><p className="workplace-kicker">Оператор 112 · учебный входящий вызов</p><h2>Карточка № {item.card.number}</h2></div>
      <div className="intake-elapsed">В работе: {Math.floor(elapsed / 60)}:{String(elapsed % 60).padStart(2, "0")}</div>
    </header>
    {item.interruptions.length > 0 && <p role="alert" className="notice">После перезапуска сервера состояние вызова и карточки восстановлено.</p>}
    <section className="intake-phone" aria-label="Входящий вызов">
      <div><span className="intake-phone-icon" aria-hidden="true">☎</span><div><strong>{item.card.aon}</strong><p>Сценарное время {item.card.call_local_time} · {item.card.call_time_zone}</p></div></div>
      <span className={`status-badge intake-call-${state.call_status}`}>{state.call_status === "ringing" ? "Входящий" : state.call_status === "connected" ? "Разговор" : "Разговор завершён"}</span>
      {item.state === "offered" && <button type="button" disabled={!!pending} onClick={() => send("open", {})}>Открыть вызов</button>}
      {item.state === "opened" && state.call_status === "ringing" && <button type="button" className="arm-primary-action" disabled={!!pending} onClick={() => send("answer_incoming", {})}>Ответить</button>}
      {state.call_status === "connected" && !terminal && <button type="button" disabled={!!pending} onClick={() => send("end_incoming", {})}>Завершить разговор</button>}
    </section>
    {state.call_status !== "ringing" && <section className="intake-transcript"><h3>Заявитель</h3><ol>{state.transcript.map((line, index) => <li key={index}>{line.text}</li>)}</ol></section>}
    {state.call_status !== "ringing" && <form className="intake-form" onSubmit={save}>
      <h3>Основная карточка</h3>
      <p>Отмечайте «неизвестно» только если заявитель прямо не знает сведения. Пустое поле означает, что сведения ещё не внесены.</p>
      <div className="intake-fields">
        <Field label="ФИО заявителя" field={draft.applicant_name} disabled={terminal || state.dispatched} onChange={(v) => update("applicant_name", v)} />
        <Field label="Кем приходится пострадавшему" field={draft.applicant_status} disabled={terminal || state.dispatched} choices={[{ value: "victim", label: "Сам пострадавший" }, { value: "relative", label: "Родственник" }, { value: "witness", label: "Очевидец" }]} onChange={(v) => update("applicant_status", v)} />
        <Field label="Возраст" field={draft.age} disabled={terminal || state.dispatched} numeric onChange={(v) => update("age", v)} />
        <Field label="Город" field={draft.address.city} disabled={terminal || state.dispatched} onChange={(v) => updateAddress("city", v)} />
        <Field label="Улица" field={draft.address.street} disabled={terminal || state.dispatched} onChange={(v) => updateAddress("street", v)} />
        <Field label="Дом" field={draft.address.house} disabled={terminal || state.dispatched} onChange={(v) => updateAddress("house", v)} />
        <Field label="Корпус" field={draft.address.building} disabled={terminal || state.dispatched} onChange={(v) => updateAddress("building", v)} />
        <Field label="Квартира" field={draft.address.flat} disabled={terminal || state.dispatched} onChange={(v) => updateAddress("flat", v)} />
        <Field label="Ориентир" field={draft.address.landmark} disabled={terminal || state.dispatched} onChange={(v) => updateAddress("landmark", v)} />
        <Field label="Тип обращения" field={draft.incident_type} disabled={terminal || state.dispatched} choices={[{ value: "medical_assistance_request", label: "Медицинская помощь" }]} onChange={(v) => update("incident_type", v)} />
        <Field label="Жалобы" field={draft.complaint} disabled={terminal || state.dispatched} multiline onChange={(v) => update("complaint", v)} />
        <Field label="Есть пострадавшие" field={draft.victims_present} disabled={terminal || state.dispatched} negative choices={[{ value: "yes", label: "Да" }]} onChange={(v) => update("victims_present", v)} />
        <Field label="Число пострадавших" field={draft.victims_count} disabled={terminal || state.dispatched} numeric onChange={(v) => update("victims_count", v)} />
        <Field label="Телефон со слов заявителя" field={draft.provided_phone} disabled={terminal || state.dispatched} onChange={(v) => update("provided_phone", v)} />
      </div>
      {!terminal && !state.dispatched && <button type="submit" disabled={!!pending || (state.has_saved_draft && !dirty)}>Сохранить карточку</button>}
      {state.has_saved_draft && <p className="notice">{dirty ? "Есть изменения после последнего сохранения." : "Черновик сохранён на сервере."}</p>}
    </form>}
    {state.call_status !== "ringing" && <section className="intake-dispatch">
      <h3>Направление карточки</h3>
      {item.dispatch ? <p>Направлена в {item.dispatch.service_code} · {formatDateTime(item.dispatch.sent_at)}. Снимок отправки сохранён.</p> : <>
        <label>Учебная служба<select value={serviceCode} onChange={(event) => setServiceCode(event.target.value)} disabled={terminal || !!pending}>
          {item.recipient_services.map((code) => <option key={code} value={code}>{code === "pilot_ambulance" ? "Учебная скорая помощь" : code}</option>)}
        </select></label>
        <button type="button" disabled={terminal || !!pending || !state.has_saved_draft || dirty || !serviceCode} onClick={() => send("dispatch_intake", { service_code: serviceCode })}>Направить сохранённую карточку</button>
        {dirty && <p>Сохраните изменения перед отправкой.</p>}
      </>}
    </section>}
    {!terminal && state.call_status === "ended" && state.dispatched && <button type="button" className="arm-primary-action" disabled={!!pending} onClick={() => send("complete_intake", {})}>Завершить обработку</button>}
    {terminal && <p className="notice">{item.state === "interrupted" ? "Занятие остановлено; карточка закрыта." : "Обработка завершена. Результат появится после оценки преподавателя."}</p>}
    {!storage && <p role="alert" className="error">Локальное хранилище недоступно: автоматический повтор команды после сбоя не гарантируется.</p>}
    {pending && <p className="notice">Действие сохраняется…</p>}
    {pending && error && <button type="button" onClick={() => void deliver(pending)}>Повторить отправку</button>}
    {error && <p role="alert" className="error">{errorMessage(error)}</p>}
    {rejection && <p role="alert" className="error">{rejection}</p>}
    {receipt?.outcome === "applied" && <p role="status">Действие сохранено{receipt.replayed ? " после восстановления" : ""}.</p>}
  </section>;
}

function Field({ label, field, onChange, disabled, choices, numeric, negative, multiline }: {
  label: string; field: IntakeField; onChange: (value: IntakeField) => void; disabled: boolean;
  choices?: Array<{ value: string; label: string }>; numeric?: boolean; negative?: boolean; multiline?: boolean;
}) {
  const changeState = (state: IntakeField["state"]) => onChange(state === "known" ? { state, value: choices?.[0]?.value ?? "" } : { state });
  return <div className="intake-field">
    <label>{label}<select value={field.state} disabled={disabled} onChange={(event) => changeState(event.target.value as IntakeField["state"])}>
      <option value="unanswered">Не заполнено</option><option value="known">Известно</option><option value="unknown">Неизвестно</option>{negative && <option value="negative">Нет</option>}
    </select></label>
    {field.state === "known" && (choices ? <select aria-label={`${label}: значение`} value={field.value ?? ""} disabled={disabled} onChange={(event) => onChange({ state: "known", value: event.target.value })}>{choices.map((choice) => <option key={choice.value} value={choice.value}>{choice.label}</option>)}</select>
      : multiline ? <textarea aria-label={`${label}: значение`} value={field.value ?? ""} disabled={disabled} maxLength={1000} required onChange={(event) => onChange({ state: "known", value: event.target.value })} />
      : <input aria-label={`${label}: значение`} type={numeric ? "number" : "text"} min={numeric ? 0 : undefined} value={field.value ?? ""} disabled={disabled} maxLength={1000} required onChange={(event) => onChange({ state: "known", value: event.target.value })} />)}
  </div>;
}
