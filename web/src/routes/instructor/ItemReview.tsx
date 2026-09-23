import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { assessmentQueryKey, createAssessmentRevision, useAssessment, type CriterionResult } from "../../api/assessment";
import { ApiError, api } from "../../api/client";
import { errorMessage } from "../../api/errors";
import type { components } from "../../api/schema";
import { formatDateTime } from "../../format";

type CriterionStatus = CriterionResult["status"];
type Item = components["schemas"]["Item"];
type IntakeField = { state: string; value?: string };
type IntakeCard = { number: string; aon: string; call_local_time: string; call_time_zone: string; applicant_name: IntakeField; applicant_status: IntakeField; age: IntakeField; address: Record<string, IntakeField>; incident_type: IntakeField; complaint: IntakeField; victims_present: IntakeField; victims_count: IntakeField; provided_phone: IntakeField; on_site_phone?: IntakeField; channel?: IntakeField; foreign_language?: IntakeField; no_on_site?: IntakeField; no_access?: IntakeField };
type IntakeReviewItem = Item & { card: IntakeCard; intake_reference?: unknown; intake_state?: { transcript?: Array<{ text: string; server_at: string }> }; dispatch?: { service_code: string; sent_at: string; card_snapshot: IntakeCard } };
type IntakeReviewEvidence = { final_card?: IntakeCard; dispatch?: { service_code: string; sent_at: string; card_snapshot: IntakeCard }; intake_state?: { transcript?: Array<{ text: string; server_at: string }> } };
const labels: Record<string, string> = { met: "выполнено", partial: "частично", not_met: "не выполнено", not_applicable: "не применимо", unavailable: "не проверено" };
const manualStatuses: CriterionStatus[] = ["met", "partial", "not_met", "not_applicable"];

export function ItemReviewRoute() {
  const { itemId = "" } = useParams();
  const review = useAssessment(itemId);
  const item = useItem(itemId);
  const client = useQueryClient();
  const [reason, setReason] = useState("");
  const [override, setOverride] = useState("");
  const [changes, setChanges] = useState<Record<string, CriterionStatus>>({});
  const sourceCriteria = useMemo(() => review.data?.final?.criteria ?? review.data?.rubric_effective.criteria?.filter((c) => !c.disabled).map((c) => ({ id: c.id, status: "not_met" as CriterionStatus, weight: c.weight ?? 0, critical: !!c.critical, evidence_refs: [], explanation: "" })) ?? [], [review.data]);
  const mutation = useMutation({ mutationFn: () => createAssessmentRevision(itemId, { reason, base_revision: review.data?.final?.revision ?? 0, score_override: override === "" ? undefined : Number(override), criteria: sourceCriteria.map((c) => ({ ...c, status: changes[c.id] ?? (c.status === "unavailable" ? "not_met" : c.status) })) }), onSuccess: async () => { setReason(""); setOverride(""); setChanges({}); await client.invalidateQueries({ queryKey: assessmentQueryKey(itemId) }); } });
  if (review.isPending || item.isPending) return <p>Загрузка…</p>;
  if (review.isError) return <p className="error">{errorMessage(review.error)}</p>;
  if (item.isError) return <p className="error">{errorMessage(item.error)}</p>;
  const detail = review.data;
  const evidence = detail.evidence;
  const isIntake = item.data.exercise_type === "operator112_intake";
  const stale = mutation.error instanceof ApiError && mutation.error.code === "stale_revision";
  return <section>
    <p><Link to="/instructor/lessons">← К занятиям</Link></p>
    <h1>Разбор карточки № {item.data.card_number}</h1>
	<p>{isIntake ? "Оценка преподавателя" : `Автооценка: ${detail.automatic_state ?? "нет"}`}; итог: {detail.final ? `${detail.final.status}${detail.final.score == null ? "" : ` · ${detail.final.score.toFixed(1)}`}` : "ещё нет"}</p>
	{isIntake ? <IntakeReviewPanel item={item.data as unknown as IntakeReviewItem} evidence={evidence as IntakeReviewEvidence} /> : <>
		<h2>Карточка и эталон</h2>
		<p>{(item.data.card as { applicant?: { name?: string }; address?: { text?: string } })?.applicant?.name ?? "Заявитель"} · {(item.data.card as { address?: { text?: string } })?.address?.text ?? "адрес не указан"}</p>
		<details><summary>Эталон сценария</summary><pre>{JSON.stringify(item.data.reference ?? {}, null, 2)}</pre></details>
		<h2>Автоматическая проверка</h2>
		<CriteriaTable criteria={detail.final?.kind === "auto" ? detail.final.criteria : detail.revisions.find((r) => r.kind === "auto")?.criteria ?? []} />
	</>}
    <h2>Журнал и звонки</h2>
    <ul>{evidence.actions?.map((action) => <li key={action.action_id}>{formatDateTime(action.server_at)} · {action.type} · {action.accepted ? "принято" : "отклонено"}</li>)}</ul>
    {evidence.comments?.length ? <><h3>Комментарии</h3><ul>{evidence.comments.map((comment) => <li key={comment.seq}>{comment.text}</li>)}</ul></> : null}
    {evidence.events?.length ? <><h3>События</h3><ul>{evidence.events.map((event) => <li key={event.key}>{event.key}: {event.state}{event.late ? " (поздно)" : ""}</li>)}</ul></> : null}
	{!isIntake && (evidence.calls?.length ? <><h3>Звонки</h3>{evidence.calls.map((call) => <div key={call.call_id}><p>{call.contact_key}: {call.accepted_by ?? "не завершён"} — {call.summary ?? ""}</p>{call.recording_sha256 && <audio controls src={`/api/v1/items/${encodeURIComponent(itemId)}/calls/${encodeURIComponent(call.call_id)}/recording`} />}</div>)}</> : <p>Звонков нет.</p>)}
    <h2>История ревизий</h2>
    <ol>{detail.revisions.map((revision) => <li key={revision.id}>rev {revision.revision} · {revision.kind} · {revision.status} · {formatDateTime(revision.created_at)}{revision.reason ? ` — ${revision.reason}` : ""}</li>)}</ol>
    <form className="lesson-form" onSubmit={(event) => { event.preventDefault(); mutation.mutate(); }}>
      <h2>Экспертная оценка</h2>
      <p>«Не проверено» нужно разрешить вручную, прежде чем сохранить итог.</p>
      <table><thead><tr><th>Критерий</th><th>Статус</th></tr></thead><tbody>{sourceCriteria.map((criterion) => <tr key={criterion.id}><td>{criterion.id}</td><td><select value={changes[criterion.id] ?? (criterion.status === "unavailable" ? "not_met" : criterion.status)} onChange={(event) => setChanges({ ...changes, [criterion.id]: event.target.value as CriterionStatus })}>{manualStatuses.map((status) => <option key={status} value={status}>{labels[status]}</option>)}</select></td></tr>)}</tbody></table>
      <label>Причина<textarea required minLength={3} maxLength={2000} value={reason} onChange={(event) => setReason(event.target.value)} /></label>
      <label>Итоговый балл (необязательно)<input type="number" min="0" max="100" step="0.01" value={override} onChange={(event) => setOverride(event.target.value)} /></label>
      {stale && <p role="alert" className="error">Оценка изменилась, обновите страницу.</p>}
      {mutation.isError && !stale && <p role="alert" className="error">{errorMessage(mutation.error)}</p>}
      <p><button type="submit" disabled={mutation.isPending || sourceCriteria.length === 0}>Сохранить экспертную оценку</button></p>
    </form>
  </section>;
}

function useItem(itemId: string) { return useQuery({ queryKey: ["training", "item", itemId], queryFn: () => api.get<Item>(`/items/${encodeURIComponent(itemId)}`), enabled: itemId !== "" }); }

function CriteriaTable({ criteria }: { criteria: CriterionResult[] }) { if (criteria.length === 0) return <p>Автооценка ещё не готова.</p>; return <table><thead><tr><th>Критерий</th><th>Статус</th><th>Основание</th></tr></thead><tbody>{criteria.map((criterion) => <tr key={criterion.id}><td>{criterion.id}{criterion.critical ? " · критичный" : ""}</td><td>{labels[criterion.status]}</td><td>{criterion.explanation || "—"}{criterion.evidence_refs?.length ? ` (${criterion.evidence_refs.join(", ")})` : ""}</td></tr>)}</tbody></table>; }

function IntakeReviewPanel({ item, evidence }: { item: IntakeReviewItem; evidence: IntakeReviewEvidence }) {
  const card = evidence.final_card ?? item.card;
  const dispatch = evidence.dispatch ?? item.dispatch;
  return <>
    <h2>Разговор с заявителем</h2>
    <ol>{(evidence.intake_state?.transcript ?? item.intake_state?.transcript ?? []).map((line, index) => <li key={index}>{line.text}</li>)}</ol>
    <h2>Итоговая карточка</h2>
    <IntakeCardView card={card} />
    <h2>Передача службе</h2>
    {dispatch ? <><p>Адресат: {dispatch.service_code === "pilot_ambulance" ? "03 · Скорая помощь" : dispatch.service_code} · отправлено {formatDateTime(dispatch.sent_at)}</p><p>Снимок на момент отправки:</p><IntakeCardView card={dispatch.card_snapshot} /></> : <p>Карточка не направлена.</p>}
    <details><summary>Эталон сценария</summary><pre>{JSON.stringify(item.intake_reference ?? {}, null, 2)}</pre></details>
  </>;
}

function IntakeCardView({ card }: { card: IntakeCard }) {
  const value = (field: IntakeField | undefined) => field?.state === "known" ? field.value === "yes" ? "да" : field.value : field?.state === "unknown" ? "неизвестно" : field?.state === "negative" ? "нет" : "не заполнено";
  const addressFields = [
    ["country", "Страна"], ["region", "Субъект"], ["city", "Населённый пункт"], ["object", "Объект"],
    ["okrug", "Округ"], ["district", "Район"], ["street", "Улица"], ["house", "Дом"],
    ["building", "Корпус"], ["structure", "Строение"], ["flat", "Квартира / офис"],
    ["entrance", "Подъезд"], ["floor", "Этаж"], ["code", "Код"], ["landmark", "Ориентир"],
    ["descriptive", "Описательный адрес"],
  ] as const;
  return <dl className="intake-review-card">
    <dt>Номер</dt><dd>{card.number}</dd><dt>АОН</dt><dd>{card.aon}</dd><dt>Время вызова</dt><dd>{card.call_local_time} ({card.call_time_zone})</dd>
    <dt>Заявитель</dt><dd>{value(card.applicant_name)} · {value(card.applicant_status)} · возраст {value(card.age)}</dd>
    <dt>Канал и язык</dt><dd>{value(card.channel)} · иностранный язык: {value(card.foreign_language)}</dd>
    <dt>Адрес</dt><dd>{addressFields.map(([key, label]) => <div key={key}>{label}: {value(card.address[key])}</div>)}</dd>
    <dt>Тип</dt><dd>{value(card.incident_type)}</dd><dt>Жалоба</dt><dd>{value(card.complaint)}</dd>
    <dt>Пострадавшие</dt><dd>{value(card.victims_present)} · {value(card.victims_count)}</dd>
    <dt>Телефоны</dt><dd>Предоставленный: {value(card.provided_phone)} · На место: {value(card.on_site_phone)}</dd>
    <dt>Особые отметки</dt><dd>Нет на месте / отказ от скорой: {value(card.no_on_site)} · Нет доступа / заблокированные: {value(card.no_access)}</dd>
  </dl>;
}
