import { useEffect, useState, type FormEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { executeCommand, type Command, type Receipt } from "../../api/commands";
import { errorMessage } from "../../api/errors";
import type { Me } from "../../api/useMe";
import { itemQueryKey, myItemsQueryKey, myRunQueryKey } from "../../api/workplace";
import { availableLocalStorage, clearPending, loadPending, savePending, type PendingCommand } from "../../commands/pending";
import type { IntakeCard, IntakeField, IntakeItem, IntakeProfileAnswer } from "./Operator112Workplace";

const empty: IntakeField = { state: "unanswered" };
const serviceNames: Record<string, string> = {
  pilot_gas_104: "104 · Газовая служба (учебная)",
  pilot_fire_101: "101 · Пожарная служба (учебная)",
  pilot_ambulance: "03 · Скорая помощь (учебная)",
};
const addressFields: Array<[keyof IntakeCard["address"], string]> = [
  ["country", "Страна"], ["region", "Субъект"], ["city", "Населённый пункт"], ["object", "Объект"],
  ["okrug", "Округ"], ["district", "Район"], ["street", "Улица"], ["house", "Дом"],
  ["building", "Корпус"], ["structure", "Строение"], ["flat", "Квартира / офис"],
  ["entrance", "Подъезд"], ["floor", "Этаж"], ["code", "Код"], ["landmark", "Ориентир"],
  ["descriptive", "Описательный адрес"],
];

function BasicField({ label, field, onChange, disabled, multiline }: { label: string; field: IntakeField; onChange: (value: IntakeField) => void; disabled: boolean; multiline?: boolean }) {
  return <label className="intake-field"><span>{label}</span><div className="intake-field-control">
    {multiline ? <textarea aria-label={`${label}: значение`} value={field.state === "known" ? field.value ?? "" : ""} maxLength={1999} disabled={disabled} onChange={(event) => onChange(event.target.value ? { state: "known", value: event.target.value } : empty)} />
      : <input aria-label={`${label}: значение`} value={field.state === "known" ? field.value ?? "" : ""} maxLength={1000} disabled={disabled} onChange={(event) => onChange(event.target.value ? { state: "known", value: event.target.value } : empty)} />}
    <select aria-label={`${label}: статус`} value={field.state === "unknown" ? "unknown" : field.state === "known" ? "known" : "unanswered"} disabled={disabled}
      onChange={(event) => onChange(event.target.value === "unknown" ? { state: "unknown" } : event.target.value === "known" && field.value ? { state: "known", value: field.value } : empty)}>
      <option value="unanswered">—</option><option value="known">Известно</option><option value="unknown">Неизвестно</option>
    </select>
  </div></label>;
}

export function Operator112ProfileCase({ me, item }: { me: Me; item: IntakeItem }) {
  const client = useQueryClient();
  const [storage] = useState(() => availableLocalStorage());
  const [pending, setPending] = useState<PendingCommand | null>(() => storage ? loadPending(storage, me.user.id, item.id) : null);
  const [receipt, setReceipt] = useState<Receipt | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [draft, setDraft] = useState<IntakeCard>(item.card);
  const [typeToAdd, setTypeToAdd] = useState("");
  const [selectedServices, setSelectedServices] = useState<string[]>([]);
  const [reviewReason, setReviewReason] = useState("");
  const state = item.intake_state;
  const catalog = state.catalog;
  const terminal = item.state === "closed" || item.state === "interrupted";
  const editable = !terminal && item.state !== "offered";
  const dirty = JSON.stringify(draft) !== JSON.stringify(item.card);
  const suggested = state.suggested_services ?? [];

  useEffect(() => {
    setDraft(item.card);
    setSelectedServices(state.service_review?.selected ?? suggested.map((entry) => entry.service_code));
    setReviewReason(state.service_review?.reason ?? "");
  }, [item.seq]); // eslint-disable-line react-hooks/exhaustive-deps

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
  useEffect(() => { if (pending) queueMicrotask(() => { void deliver(pending); }); }, []); // eslint-disable-line react-hooks/exhaustive-deps
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
  const updateAnswer = (profileID: string, fieldID: string, answer: IntakeProfileAnswer) => setDraft((current) => ({
    ...current, profiles: { ...current.profiles, [profileID]: { ...current.profiles![profileID],
      answers: { ...current.profiles![profileID].answers, [fieldID]: answer } } },
  }));
  const save = (event: FormEvent) => { event.preventDefault(); send("save_intake_draft", { draft }); };
  const toggleFlag = (key: "no_on_site" | "no_access") => update(key, draft[key]?.state === "known" ? empty : { state: "known", value: "yes" });
  const changeService = (code: string) => setSelectedServices((current) => current.includes(code) ? current.filter((entry) => entry !== code) : [...current, code]);
  const allServices = Array.from(new Set(catalog?.service_rules.map((rule) => rule.service_code) ?? []));
  const selectionChanged = selectedServices.length !== suggested.length || selectedServices.some((code) => !suggested.some((entry) => entry.service_code === code));

  return <section className="intake-workplace intake-profile-case">
    <header className="intake-console"><div className="intake-console-icon" aria-hidden="true">112</div>
      <div className="intake-console-incident"><strong>Происшествие {item.card.number}</strong><span>Учебная карточка без разговора</span><span>Оператор: {me.user.full_name}</span></div>
      {item.state === "offered" && <button type="button" disabled={!!pending} onClick={() => send("open", {})}>Открыть кейс</button>}
    </header>
    {item.interruptions.length > 0 && <p role="alert" className="notice">Состояние карточки восстановлено после перезапуска.</p>}
    {editable || terminal ? <>
      <form id="profile-case-form" className="intake-main" onSubmit={save}>
        <div className="intake-left">
          <section className="intake-panel intake-applicant"><h3>Заявитель</h3>
            <BasicField label="ФИО заявителя" field={draft.applicant_name} disabled={!editable} onChange={(value) => update("applicant_name", value)} />
            <BasicField label="Статус заявителя" field={draft.applicant_status} disabled={!editable} onChange={(value) => update("applicant_status", value)} />
            <BasicField label="Канал связи" field={draft.channel} disabled={!editable} onChange={(value) => update("channel", value)} />
            <BasicField label="Телефон со слов заявителя" field={draft.provided_phone} disabled={!editable} onChange={(value) => update("provided_phone", value)} />
            <BasicField label="Телефон на месте" field={draft.on_site_phone ?? empty} disabled={!editable} onChange={(value) => update("on_site_phone", value)} />
          </section>
          <section className="intake-panel intake-address"><h3>Адрес</h3><div className="intake-address-grid">
            {addressFields.map(([key, label]) => <BasicField key={key} label={label} field={draft.address[key]} disabled={!editable} onChange={(value) => updateAddress(key, value)} />)}
          </div></section>
          <section className="intake-panel intake-description"><h3>Описание со слов заявителя</h3>
            <BasicField label="Описание" field={draft.complaint} disabled={!editable} multiline onChange={(value) => update("complaint", value)} />
          </section>
        </div>
        <div className="intake-right">
          <section className="intake-panel"><h3>Общие сведения</h3>
            <div className="intake-victims-buttons">
              <button type="button" aria-pressed={draft.victims_present.state === "known"} disabled={!editable} onClick={() => update("victims_present", { state: "known", value: "yes" })}>Пострадавшие</button>
              <button type="button" aria-pressed={draft.victims_present.state === "negative"} disabled={!editable} onClick={() => update("victims_present", { state: "negative" })}>Нет пострадавших</button>
            </div>
            <BasicField label="Число пострадавших" field={draft.victims_count} disabled={!editable} onChange={(value) => update("victims_count", value)} />
          </section>
          <section className="intake-panel"><h3>Добавить тип происшествия</h3>
            <div className="intake-type-picker"><select aria-label="Тип происшествия" value={typeToAdd} disabled={!editable || dirty || !!pending} onChange={(event) => setTypeToAdd(event.target.value)}>
              <option value="">Выберите тип</option>
              {catalog?.types.filter((type) => !(draft.incident_types ?? []).includes(type.id)).map((type) => <option key={type.id} value={type.id}>{type.name}</option>)}
            </select><button type="button" disabled={!editable || dirty || !!pending || !typeToAdd} onClick={() => { send("add_incident_type", { type_id: typeToAdd }); setTypeToAdd(""); }}>Добавить</button></div>
            {dirty && <p>Сохраните изменения карточки перед изменением типа.</p>}
            <ul>{(draft.incident_types ?? []).map((id) => <li key={id}>{catalog?.types.find((type) => type.id === id)?.name ?? id} <button type="button" disabled={!editable || dirty || !!pending}
              onClick={() => { if (window.confirm("Убрать тип происшествия? Ответы его карты сохранятся для восстановления, но не войдут в итоговую карточку и предложение служб.")) send("remove_incident_type", { type_id: id }); }}>Убрать</button></li>)}</ul>
          </section>
          {catalog?.profiles.filter((profile) => !!draft.profiles?.[profile.id]).map((profile) => <section className="intake-panel intake-profile-panel" key={profile.id}>
            <h3>{profile.name}</h3>{profile.fields.map((field) => <div className="intake-profile-row" key={field.id}><strong>{field.label}</strong>
              {field.kind === "shared" ? <button type="button" aria-pressed={draft[field.shared!]?.state === "known"} disabled={!editable} onClick={() => toggleFlag(field.shared!)}>{field.label === "Пострадавший" ? "Пострадавший не на месте / Отказ от Скорой" : "Нет доступа"}</button>
                : <ProfileAnswerField label={field.label} kind={field.kind} options={field.options ?? []} answer={draft.profiles![profile.id].answers[field.id]}
                  disabled={!editable} onChange={(answer) => updateAnswer(profile.id, field.id, answer)} />}
            </div>)}</section>)}
          <section className="intake-panel intake-services"><h3>Службы</h3>
            <p>Предложено по выбранным типам и сохранённым признакам:</p>
            {suggested.length ? <ul>{suggested.map((entry) => <li key={entry.service_code}><strong>{serviceNames[entry.service_code] ?? entry.service_code}</strong> — {entry.reasons.join("; ")}</li>)}</ul> : <p>Пока нет предложений.</p>}
            <fieldset disabled={!editable || !state.has_saved_draft || dirty || !!pending}><legend>Итоговый выбор оператора</legend>
              {allServices.map((code) => <label key={code}><input type="checkbox" checked={selectedServices.includes(code)} onChange={() => changeService(code)} /> {serviceNames[code] ?? code}</label>)}
              <label>Причина изменения предложения <input aria-label="Причина изменения предложения" value={reviewReason} maxLength={1000} onChange={(event) => setReviewReason(event.target.value)} /></label>
              <button type="button" disabled={selectionChanged && !reviewReason.trim()} onClick={() => send("review_service_selection", { services: selectedServices, reason: reviewReason.trim() })}>Зафиксировать выбор служб</button>
            </fieldset>
            {state.service_review && <p>Итоговый выбор зафиксирован: {state.service_review.selected.map((code) => serviceNames[code] ?? code).join(", ") || "службы не выбраны"}.</p>}
          </section>
        </div>
      </form>
      <footer className="intake-action-bar"><div className="intake-actions">
        {!terminal && <button type="submit" form="profile-case-form" disabled={!!pending || !dirty && state.has_saved_draft}>Сохранить карточку</button>}
        {!terminal && <button type="button" disabled={!!pending || dirty || !state.has_saved_draft || !state.service_review || !(draft.incident_types?.length)} onClick={() => send("complete_profile_case", {})}>Завершить кейс</button>}
      </div></footer>
      <div className="intake-feedback">{state.has_saved_draft && <p>{dirty ? "Есть несохранённые изменения." : "Черновик сохранён."}</p>}
        {terminal && <p>{item.state === "interrupted" ? "Занятие остановлено." : "Кейс завершён. Результат появится после оценки преподавателя."}</p>}</div>
    </> : <p className="intake-waiting">Откройте кейс, чтобы выбрать тип происшествия.</p>}
    {!storage && <p role="alert" className="error">Локальное хранилище недоступно: автоматический повтор команды после сбоя не гарантируется.</p>}
    {pending && <p className="notice">Действие сохраняется…</p>}
    {pending && error && <button type="button" onClick={() => void deliver(pending)}>Повторить отправку</button>}
    {error && <p role="alert" className="error">{errorMessage(error)}</p>}
    {receipt?.outcome === "rejected" && <p role="alert" className="error">{receipt.error_code}</p>}
    {receipt?.outcome === "applied" && <p role="status">Действие сохранено{receipt.replayed ? " после восстановления" : ""}.</p>}
  </section>;
}

function ProfileAnswerField({ label, kind, options, answer, disabled, onChange }: {
  label: string; kind: "single" | "multiple" | "text"; options: string[]; answer: IntakeProfileAnswer; disabled: boolean; onChange: (answer: IntakeProfileAnswer) => void;
}) {
  if (kind === "text") return <div className="intake-profile-options"><textarea aria-label={`${label}: значение`} disabled={disabled || answer.state === "unknown"} value={answer.state === "known" ? answer.value ?? "" : ""} maxLength={1999}
    onChange={(event) => onChange(event.target.value ? { state: "known", value: event.target.value } : { state: "unanswered" })} />
    <button type="button" aria-pressed={answer.state === "unknown"} disabled={disabled} onClick={() => onChange(answer.state === "unknown" ? { state: "unanswered" } : { state: "unknown" })}>Неизвестно</button></div>;
  return <div className="intake-profile-options">{options.map((option) => {
    const selected = kind === "single" ? answer.state === "known" && answer.value === option : answer.state === "known" && (answer.values ?? []).includes(option);
    return <button type="button" key={option} aria-pressed={selected} disabled={disabled} onClick={() => {
      if (kind === "single") onChange(selected ? { state: "unanswered" } : { state: "known", value: option });
      else { const values = selected ? (answer.values ?? []).filter((value) => value !== option) : [...(answer.state === "known" ? answer.values ?? [] : []), option]; onChange(values.length ? { state: "known", values } : { state: "unanswered" }); }
    }}>{option}</button>;
  })}<button type="button" aria-pressed={answer.state === "unknown"} disabled={disabled} onClick={() => onChange(answer.state === "unknown" ? { state: "unanswered" } : { state: "unknown" })}>Неизвестно</button></div>;
}
