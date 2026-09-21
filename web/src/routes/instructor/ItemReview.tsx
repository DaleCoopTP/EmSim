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
  const stale = mutation.error instanceof ApiError && mutation.error.code === "stale_revision";
  return <section>
    <p><Link to="/instructor/lessons">← К занятиям</Link></p>
    <h1>Разбор карточки № {item.data.card_number}</h1>
    <p>Автооценка: {detail.automatic_state ?? "нет"}; итог: {detail.final ? `${detail.final.status}${detail.final.score == null ? "" : ` · ${detail.final.score.toFixed(1)}`}` : "ещё нет"}</p>
    <h2>Карточка и эталон</h2>
    <p>{item.data.card?.applicant?.name ?? "Заявитель"} · {item.data.card?.address?.text ?? "адрес не указан"}</p>
    <details><summary>Эталон сценария</summary><pre>{JSON.stringify(item.data.reference ?? {}, null, 2)}</pre></details>
    <h2>Автоматическая проверка</h2>
    <CriteriaTable criteria={detail.final?.kind === "auto" ? detail.final.criteria : detail.revisions.find((r) => r.kind === "auto")?.criteria ?? []} />
    <h2>Журнал и звонки</h2>
    <ul>{evidence.actions?.map((action) => <li key={action.action_id}>{formatDateTime(action.server_at)} · {action.type} · {action.accepted ? "принято" : "отклонено"}</li>)}</ul>
    {evidence.comments?.length ? <><h3>Комментарии</h3><ul>{evidence.comments.map((comment) => <li key={comment.seq}>{comment.text}</li>)}</ul></> : null}
    {evidence.events?.length ? <><h3>События</h3><ul>{evidence.events.map((event) => <li key={event.key}>{event.key}: {event.state}{event.late ? " (поздно)" : ""}</li>)}</ul></> : null}
    {evidence.calls?.length ? <><h3>Звонки</h3>{evidence.calls.map((call) => <div key={call.call_id}><p>{call.contact_key}: {call.accepted_by ?? "не завершён"} — {call.summary ?? ""}</p>{call.recording_sha256 && <audio controls src={`/api/v1/items/${encodeURIComponent(itemId)}/calls/${encodeURIComponent(call.call_id)}/recording`} />}</div>)}</> : <p>Звонков нет.</p>}
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
