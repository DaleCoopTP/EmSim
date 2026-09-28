import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMemo, useState, type ReactNode } from "react";
import { Link, useParams } from "react-router-dom";
import { assessmentQueryKey, createAssessmentRevision, useAssessment, type CriterionResult } from "../../api/assessment";
import { ApiError, api } from "../../api/client";
import { errorMessage } from "../../api/errors";
import type { components } from "../../api/schema";
import { formatDateTime } from "../../format";
import { cardStatusLabel, reactionLabel } from "../../labels";
import { IntakeAutoAssessment, isPenaltyCriterion, criterionStatusLabels, type RubricEffectiveCriterion } from "../../components/IntakeAutoAssessment";

type CriterionStatus = CriterionResult["status"];
type Item = components["schemas"]["Item"];
type IntakeField = { state: string; value?: string };
type IntakeCard = { number: string; aon: string; call_local_time: string; call_time_zone: string; applicant_name: IntakeField; applicant_status: IntakeField; age: IntakeField; address: Record<string, IntakeField>; incident_type: IntakeField; incident_types?: string[]; profiles?: Record<string, { definition_id: string; version: number; answers: Record<string, { state: string; value?: string; values?: string[] }> }>; complaint: IntakeField; victims_present: IntakeField; victims_count: IntakeField; provided_phone: IntakeField; on_site_phone?: IntakeField; channel?: IntakeField; foreign_language?: IntakeField; no_on_site?: IntakeField; no_access?: IntakeField };
type IntakeReviewLine = { id?: string; speaker?: "caller" | "operator"; text: string; server_at: string; reveals?: string[]; topic_id?: string };
type IntakeReviewGeneration = { model: string; prompt_version: string; temperature?: number; top_p?: number; repeat_penalty?: number; max_tokens?: number };
type IntakeReviewCallerTurn = { turn: number; operator_line_id?: string; status: "pending" | "answered" | "cancelled" | "failed"; adapter?: string; source?: "opening" | "scripted" | "model" | "fallback" | "stub"; generation?: IntakeReviewGeneration; requested_at: string; resolved_at?: string; reason?: string };
type IntakeReviewAction = { type: string; accepted: boolean; server_at: string; log_seq: number; payload?: { draft?: IntakeCard; type_id?: string; services?: string[]; reason?: string }; effect?: { suggested?: { service_code: string; reasons: string[] }[] } };
type DialogueFact = { id: string; label: string; card_path: string; knowledge: "initial" | "on_question" | "unknown"; value?: string };
type ReviewCatalog = { version: number; types: { id: string; name: string }[]; profiles: { id: string; name: string; fields: { id: string; label: string; kind: string; shared?: string }[] }[] };
type ReviewServiceState = { suggested_services?: { service_code: string; reasons: string[] }[]; service_review?: { suggested: { service_code: string; reasons: string[] }[]; selected: string[]; reason?: string; reviewed_at: string } };
type IntakeNotification = { item_id: string; action_id: string; services: { service_code: string; suggested: boolean }[]; reason?: string; card_snapshot: IntakeCard; notified_at: string };
type IntakeReviewState = { mode?: string; catalog?: ReviewCatalog; transcript?: IntakeReviewLine[]; caller_mode?: "prepared" | "free_text"; caller_turns?: IntakeReviewCallerTurn[] } & ReviewServiceState;
type IntakeReviewItem = Item & { card: IntakeCard; intake_reference?: unknown; intake_dialogue_reference?: { facts: DialogueFact[] }; intake_state?: IntakeReviewState; dispatch?: { service_code: string; sent_at: string; card_snapshot: IntakeCard }; notification?: IntakeNotification };
type IntakeReviewEvidence = { final_card?: IntakeCard; dispatch?: { service_code: string; sent_at: string; card_snapshot: IntakeCard }; notification?: IntakeNotification; intake_state?: IntakeReviewState; actions?: IntakeReviewAction[] };
const labels = criterionStatusLabels;
const manualStatuses: CriterionStatus[] = ["met", "partial", "not_met", "not_applicable"];

export function ItemReviewRoute() {
  const { itemId = "" } = useParams();
  const review = useAssessment(itemId);
  const item = useItem(itemId);
  const client = useQueryClient();
  const [reason, setReason] = useState("");
  const [override, setOverride] = useState("");
  const [changes, setChanges] = useState<Record<string, CriterionStatus>>({});
  const [pointsChanges, setPointsChanges] = useState<Record<string, number>>({});
  const sourceCriteria = useMemo(() => review.data?.final?.criteria ?? review.data?.rubric_effective.criteria?.filter((c) => !c.disabled).map((c) => ({ id: c.id, status: "not_met" as CriterionStatus, weight: c.weight ?? 0, critical: !!c.critical, evidence_refs: [], explanation: "" })) ?? [], [review.data]);
  const rubricByID = useMemo(() => Object.fromEntries((review.data?.rubric_effective.criteria ?? []).map((c) => [c.id, c])), [review.data]);
  const isIntake = item.data?.exercise_type === "operator112_intake";
  const mutation = useMutation({
    mutationFn: () => createAssessmentRevision(itemId, {
      reason, base_revision: review.data?.final?.revision ?? 0, score_override: override === "" ? undefined : Number(override),
      criteria: isIntake ? buildIntakeRevisionCriteria(sourceCriteria, rubricByID, pointsChanges)
        : sourceCriteria.map((c) => ({ ...c, status: changes[c.id] ?? (c.status === "unavailable" ? "not_met" : c.status) })),
    }),
    onSuccess: async () => { setReason(""); setOverride(""); setChanges({}); setPointsChanges({}); await client.invalidateQueries({ queryKey: assessmentQueryKey(itemId) }); },
  });
  if (review.isPending || item.isPending) return <p>Загрузка…</p>;
  if (review.isError) return <p className="error">{errorMessage(review.error)}</p>;
  if (item.isError) return <p className="error">{errorMessage(item.error)}</p>;
  const detail = review.data;
  const evidence = detail.evidence;
  const stale = mutation.error instanceof ApiError && mutation.error.code === "stale_revision";
  const autoCriteria = detail.final?.kind === "auto" ? detail.final.criteria : detail.revisions.find((r) => r.kind === "auto")?.criteria ?? [];
  return <section>
    <p><Link to="/instructor/lessons">← К занятиям</Link></p>
    <h1>Разбор карточки № {item.data.card_number}</h1>
	<p>Автооценка: {detail.automatic_state ?? "нет"}; итог: {detail.final ? `${detail.final.status}${detail.final.score == null ? "" : ` · ${detail.final.score.toFixed(1)}`}` : "ещё нет"}</p>
	{detail.final?.model ? <p>Модель судьи: {detail.final.model}</p> : null}
	{isIntake ? <>
		<h2>Автоматическая оценка</h2>
		<IntakeAutoAssessment criteria={autoCriteria} rubricCriteria={rubricByID} />
		<IntakeReviewPanel item={item.data as unknown as IntakeReviewItem} evidence={evidence as unknown as IntakeReviewEvidence} />
	</> : <>
		<h2>Карточка и эталон</h2>
		<p>{(item.data.card as { applicant?: { name?: string }; address?: { text?: string } })?.applicant?.name ?? "Заявитель"} · {(item.data.card as { address?: { text?: string } })?.address?.text ?? "адрес не указан"}</p>
		<details><summary>Эталон сценария</summary><pre>{JSON.stringify(item.data.reference ?? {}, null, 2)}</pre></details>
		{item.data.card_status ? <p>Статус карточки: {cardStatusLabel(item.data.card_status)}</p> : null}
		<DDSStatusHistory actions={(evidence.actions ?? []) as DDSReviewAction[]} />
		<DDSCommsReview item={item.data} />
		<h2>Автоматическая проверка</h2>
		<CriteriaTable criteria={autoCriteria} rubricByID={rubricByID} />
	</>}
    <h2>Журнал и звонки</h2>
    <ul>{evidence.actions?.map((action) => <li key={action.action_id}>{formatDateTime(action.server_at)} · {action.type} · {action.accepted ? "принято" : "отклонено"}</li>)}</ul>
    {evidence.comments?.length ? <><h3>Комментарии</h3><ul>{evidence.comments.map((comment) => <li key={comment.seq}>{comment.text}</li>)}</ul></> : null}
    {evidence.events?.length ? <><h3>События</h3><ul>{evidence.events.map((event) => <li key={event.key}>{event.key}: {event.state}{event.late ? " (поздно)" : ""}</li>)}</ul></> : null}
	{!isIntake && (evidence.calls?.length ? <><h3>Звонки</h3>{evidence.calls.map((call) => <div key={call.call_id}><p>{call.contact_key}{(call as { direction?: string }).direction === "incoming" ? " (входящий)" : ""}: {call.accepted_by ?? "не завершён"} — {call.summary ?? ""}</p>{call.recording_sha256 && <audio controls src={`/api/v1/items/${encodeURIComponent(itemId)}/calls/${encodeURIComponent(call.call_id)}/recording`} />}</div>)}</> : <p>Звонков нет.</p>)}
    <h2>История ревизий</h2>
    <ol>{detail.revisions.map((revision) => <li key={revision.id}>rev {revision.revision} · {revision.kind} · {revision.status} · {formatDateTime(revision.created_at)}{revision.reason ? ` — ${revision.reason}` : ""}</li>)}</ol>
    <form className="lesson-form" onSubmit={(event) => { event.preventDefault(); mutation.mutate(); }}>
      <h2>Экспертная оценка</h2>
      {isIntake ? <>
        <p>Для блоков укажите набранные баллы (из максимума блока), для штрафов — начисленные штрафные баллы.</p>
        <table><thead><tr><th>Критерий</th><th>Баллы</th></tr></thead><tbody>{sourceCriteria.map((criterion) => {
          const rc = rubricByID[criterion.id];
          const penalty = isPenaltyCriterion(rubricByID, criterion.id);
          const weight = rc?.weight ?? criterion.weight;
          const defaultPoints = penalty ? criterion.penalty_points ?? 0 : criterion.score != null ? round2(criterion.score * weight) : 0;
          const value = pointsChanges[criterion.id] ?? defaultPoints;
          return <tr key={criterion.id}><td>{rc?.title ?? criterion.id}{criterion.critical ? " · критичный" : ""}</td>
            <td><input type="number" step="0.01" min={0} max={penalty ? undefined : weight} value={value}
              onChange={(event) => setPointsChanges({ ...pointsChanges, [criterion.id]: Number(event.target.value) })} />
              {penalty ? " баллов штрафа" : ` из ${weight}`}</td>
          </tr>;
        })}</tbody></table>
      </> : <>
        <p>«Не проверено» нужно разрешить вручную, прежде чем сохранить итог.</p>
        <table><thead><tr><th>Критерий</th><th>Статус</th></tr></thead><tbody>{sourceCriteria.map((criterion) => <tr key={criterion.id}><td>{rubricByID[criterion.id]?.title ?? criterion.id}</td><td><select value={changes[criterion.id] ?? (criterion.status === "unavailable" ? "not_met" : criterion.status)} onChange={(event) => setChanges({ ...changes, [criterion.id]: event.target.value as CriterionStatus })}>{manualStatuses.map((status) => <option key={status} value={status}>{labels[status]}</option>)}</select></td></tr>)}</tbody></table>
      </>}
      <label>Причина<textarea required minLength={3} maxLength={2000} value={reason} onChange={(event) => setReason(event.target.value)} /></label>
      <label>Итоговый балл (необязательно)<input type="number" min="0" max="100" step="0.01" value={override} onChange={(event) => setOverride(event.target.value)} /></label>
      {stale && <p role="alert" className="error">Оценка изменилась, обновите страницу.</p>}
      {mutation.isError && !stale && <p role="alert" className="error">{errorMessage(mutation.error)}</p>}
      <p><button type="submit" disabled={mutation.isPending || sourceCriteria.length === 0}>Сохранить экспертную оценку</button></p>
    </form>
  </section>;
}

function useItem(itemId: string) { return useQuery({ queryKey: ["training", "item", itemId], queryFn: () => api.get<Item>(`/items/${encodeURIComponent(itemId)}`), enabled: itemId !== "" }); }

// CriteriaTable's own "Критерий" column shows rubric_effective's title
// for the criterion's id (ДДС-3: dds/rubric-v1 and dds/rubric-v2 share
// the review UI, so a v2 lesson's T_PROGRESS/C_CALLS — and v1's own
// C_CALL_MADE/G_ADDRESS — read as their own titles, not bare ids), the
// bare id itself only as a fallback when rubric_effective has none.
function CriteriaTable({ criteria, rubricByID }: { criteria: CriterionResult[]; rubricByID: Record<string, RubricEffectiveCriterion> }) { if (criteria.length === 0) return <p>Автооценка ещё не готова.</p>; return <table><thead><tr><th>Критерий</th><th>Статус</th><th>Основание</th></tr></thead><tbody>{criteria.map((criterion) => <tr key={criterion.id}><td>{rubricByID[criterion.id]?.title ?? criterion.id}{criterion.critical ? " · критичный" : ""}</td><td>{labels[criterion.status]}</td><td>{criterion.explanation || "—"}{criterion.evidence_refs?.length ? ` (${criterion.evidence_refs.join(", ")})` : ""}</td></tr>)}</tbody></table>; }

function round2(value: number): number { return Math.round(value * 100) / 100; }

// buildIntakeRevisionCriteria turns the revision form's own per-criterion
// point inputs (pointsChanges, defaulting to the auto's own numbers) into
// the CriterionResult[] the API expects: a block's points become a 0..1
// score fraction of its own weight (clamped, since the input is free
// text); a penalty's points become penalty_points directly (112-6/
// ADR-026 — Score.Compute reads penalty_points, never a fractional score,
// for a kind=penalty criterion).
function buildIntakeRevisionCriteria(sourceCriteria: CriterionResult[], rubricCriteria: Record<string, RubricEffectiveCriterion>, pointsChanges: Record<string, number>): CriterionResult[] {
  return sourceCriteria.map((criterion) => {
    const rc = rubricCriteria[criterion.id];
    const weight = rc?.weight ?? criterion.weight;
    if (isPenaltyCriterion(rubricCriteria, criterion.id)) {
      const points = Math.max(0, pointsChanges[criterion.id] ?? criterion.penalty_points ?? 0);
      return { id: criterion.id, status: points > 0 ? "not_met" : "met", weight, critical: criterion.critical, penalty_points: points };
    }
    const defaultPoints = criterion.score != null ? criterion.score * weight : 0;
    const points = pointsChanges[criterion.id] ?? defaultPoints;
    const fraction = weight > 0 ? Math.min(1, Math.max(0, points / weight)) : 0;
    const status: CriterionStatus = fraction >= 1 ? "met" : fraction <= 0 ? "not_met" : "partial";
    return { id: criterion.id, status, weight, critical: criterion.critical, score: fraction };
  });
}


function IntakeReviewPanel({ item, evidence }: { item: IntakeReviewItem; evidence: IntakeReviewEvidence }) {
  const mode = item.intake_state?.mode ?? evidence.intake_state?.mode;
  if (mode === "card_only" || mode === "full_case") return <IntakeProfileReviewPanel item={item} evidence={evidence} />;
  const card = evidence.final_card ?? item.card;
  const dispatch = evidence.dispatch ?? item.dispatch;
  const transcript = evidence.intake_state?.transcript ?? item.intake_state?.transcript ?? [];
  const facts = item.intake_dialogue_reference?.facts ?? [];
  const revealed = new Set(transcript.flatMap((line) => line.reveals ?? []));
  const saved = (evidence.actions ?? []).filter((action) => action.accepted && action.type === "save_intake_draft" && action.payload?.draft);
  const saves = saved.map((action, index) => {
    const draft = action.payload!.draft!;
    const previous = index === 0 ? null : saved[index - 1].payload?.draft ?? null;
    const changes = changedIntakeFields(previous, draft);
    return { at: action.server_at, order: action.log_seq, text: `Карточка сохранена: ${changes.join(", ") || "без изменений"}` };
  });
  const timeline = [...transcript.map((line, index) => ({ at: line.server_at, order: index, text: `${line.speaker === "operator" ? "Оператор" : "Заявитель"}: ${line.text}` })), ...saves]
    .sort((a, b) => a.at.localeCompare(b.at) || a.order - b.order);
  return <>
    <h2>Разговор и сохранения карточки</h2>
    <ol>{timeline.map((event, index) => <li key={index}>{formatDateTime(event.at)} · {event.text}</li>)}</ol>
    {facts.length > 0 && <><h3>Факты сценария</h3><table><thead><tr><th>Сведения</th><th>Выяснение</th><th>Сказано заявителем</th><th>В отправленной карточке</th></tr></thead>
      <tbody>{facts.map((fact) => <tr key={fact.id}><td>{fact.label}</td><td>{revealed.has(fact.id) ? "Выяснено" : "Не выяснено"}</td>
        <td>{fact.knowledge === "unknown" ? "Заявитель не знает" : fact.value}</td><td>{intakeFieldText(cardFieldAt(dispatch?.card_snapshot ?? card, fact.card_path))}</td></tr>)}</tbody>
    </table></>}
    <h2>Итоговая карточка</h2>
    <IntakeCardView card={card} />
    <h2>Передача службе</h2>
    {dispatch ? <><p>Адресат: {dispatch.service_code === "pilot_ambulance" ? "03 · Скорая помощь" : dispatch.service_code} · отправлено {formatDateTime(dispatch.sent_at)}</p><p>Снимок на момент отправки:</p><IntakeCardView card={dispatch.card_snapshot} /></> : <p>Карточка не направлена.</p>}
    <details><summary>Эталон сценария</summary><pre>{JSON.stringify(item.intake_reference ?? {}, null, 2)}</pre></details>
  </>;
}

function IntakeProfileReviewPanel({ item, evidence }: { item: IntakeReviewItem; evidence: IntakeReviewEvidence }) {
  const card = evidence.final_card ?? item.card;
  const state = evidence.intake_state ?? item.intake_state;
  const catalog = state?.catalog;
  const notification = evidence.notification ?? item.notification;
  const isCall = state?.mode === "full_case";
  const transcript = state?.transcript ?? [];
  const typeName = (id: string) => catalog?.types.find((type) => type.id === id)?.name ?? id;
  const actions = (evidence.actions ?? []).filter((action) => action.accepted);
  const actionText = (action: IntakeReviewAction) => {
    switch (action.type) {
      case "open": return "Открыл кейс";
      case "answer_incoming": return "Ответил на вызов";
      case "hold_incoming": return "Поставил на удержание";
      case "resume_incoming": return "Вернулся к разговору";
      case "end_incoming": return "Завершил разговор";
      case "ask_intake_question": return "Задал уточняющий вопрос";
      case "add_incident_type": return `Добавил тип: ${typeName(action.payload?.type_id ?? "")}`;
      case "remove_incident_type": return `Убрал тип: ${typeName(action.payload?.type_id ?? "")}`;
      case "save_intake_draft": return "Сохранил карточку";
      case "review_service_selection": return `Исходное предложение: ${(action.effect?.suggested ?? []).map((entry) => `${entry.service_code} (${entry.reasons.join("; ")})`).join(", ") || "без служб"}; итоговый выбор: ${(action.payload?.services ?? []).join(", ") || "без служб"}${action.payload?.reason ? `; причина: ${action.payload.reason}` : ""}`;
      case "notify_services": return `Оповестил службы: ${(action.payload?.services ?? []).join(", ") || "без служб"}${action.payload?.reason ? `; причина: ${action.payload.reason}` : ""}`;
      case "complete_profile_case": case "complete_intake": return "Завершил обработку";
      case "mark_no_contact": return "Закрыл: нет контакта";
      case "mark_call_dropped": return "Закрыл: срыв звонка";
      case "send_caller_message": return "Сообщение заявителю";
      default: return action.type;
    }
  };
  const answerText = (answer: { state: string; value?: string; values?: string[] } | undefined) => answer?.state === "known" ? answer.values?.join(", ") ?? answer.value ?? "" : answer?.state === "unknown" ? "неизвестно" : "не заполнено";
  const callerTurns = state?.caller_turns ?? [];
  // 112-5b/ADR-025: source/generation are absent from evidence sealed
  // before this slice (112-5a), so both fall back to showing just the
  // adapter, exactly as this label read before.
  const sourceLabels: Record<string, string> = { opening: "вступление", scripted: "фиксированный ответ", model: "модель", fallback: "нейтральная реплика — модель недоступна", stub: "заглушка" };
  const turnStatusText = (turn: IntakeReviewCallerTurn) => {
    if (turn.status === "answered") {
      const source = turn.source ? sourceLabels[turn.source] ?? turn.source : undefined;
      const generation = turn.generation ? `${turn.generation.model} · ${turn.generation.prompt_version}` : undefined;
      const details = [source, generation].filter(Boolean).join(", ") || turn.adapter;
      return `Отвечено${details ? ` (${details})` : ""}`;
    }
    if (turn.status === "failed") return "Нет ответа — техническая причина";
    if (turn.status === "cancelled") return `Отменён: ${turn.reason === "held" ? "удержание" : turn.reason === "ended" ? "завершение" : turn.reason === "dropped" ? "срыв звонка" : turn.reason ?? "—"}`;
    return "Без ответа на момент остановки";
  };
  return <>
    <h2>{isCall ? "Кейс с разговором" : "Кейс без разговора"}</h2>
    <p>Каталог профилей: версия {catalog?.version ?? "—"}.{!isCall && " Отправка карточки в службу для этого режима не выполняется."}</p>
    {isCall && <><h3>Разговор с заявителем</h3>
      <ol>{transcript.map((line, index) => <li key={line.id ?? index}>{formatDateTime(line.server_at)} · {line.speaker === "operator" ? "Оператор" : "Заявитель"}: {line.text}</li>)}</ol>
      {callerTurns.length > 0 && <><h4>Ходы свободного диалога</h4>
        <ul>{callerTurns.map((turn) => <li key={turn.turn}>Ход {turn.turn} · {formatDateTime(turn.requested_at)} · {turnStatusText(turn)}</li>)}</ul></>}</>}
    <h3>Последовательность действий</h3><ol>{actions.map((action) => <li key={action.log_seq}>{formatDateTime(action.server_at)} · {actionText(action)}</li>)}</ol>
    <h3>Итоговая общая карточка</h3><IntakeCardView card={card} />
    <h3>Выбранные типы</h3><ul>{(card.incident_types ?? []).map((id) => <li key={id}>{typeName(id)}</li>)}</ul>
    <h3>Активные профильные карты</h3>{catalog?.profiles.filter((profile) => card.profiles?.[profile.id]).map((profile) => <section key={profile.id} className="intake-review-card"><h4>{profile.name}</h4><dl>
      {profile.fields.map((field) => <div key={field.id}><dt>{field.label}</dt><dd>{field.kind === "shared" ? intakeFieldText(field.shared === "no_on_site" ? card.no_on_site : card.no_access) : answerText(card.profiles?.[profile.id].answers[field.id])}</dd></div>)}
    </dl></section>)}
    <h3>Предложение и итоговый выбор служб</h3>
    <ul>{state?.service_review?.suggested.map((service) => <li key={service.service_code}>{service.service_code}: {service.reasons.join("; ")}</li>) ?? state?.suggested_services?.map((service) => <li key={service.service_code}>{service.service_code}: {service.reasons.join("; ")}</li>)}</ul>
    {notification
      ? <p>Оповещены: {notification.services.map((entry) => `${entry.service_code}${entry.suggested ? "" : " (добавлена вручную)"}`).join(", ") || "—"} · {formatDateTime(notification.notified_at)}{notification.reason ? ` · Причина изменения: ${notification.reason}` : ""}</p>
      : state?.service_review
        ? <p>Выбрано: {state.service_review.selected.join(", ") || "—"}{state.service_review.reason ? ` · Причина изменения: ${state.service_review.reason}` : ""}</p>
        : <p>Оповещение ещё не выполнено.</p>}
    <details><summary>Эталон кейса</summary><pre>{JSON.stringify(item.intake_reference ?? {}, null, 2)}</pre></details>
  </>;
}

function cardFieldAt(card: IntakeCard, path: string): IntakeField | undefined {
  const keys = path.split("/").filter(Boolean);
  if (keys.length === 1) return (card as unknown as Record<string, IntakeField>)[keys[0]];
  if (keys.length === 2 && keys[0] === "address") return card.address[keys[1]];
  return undefined;
}

function intakeFieldText(field: IntakeField | undefined): string {
  if (field?.state === "known") return field.value ?? "";
  if (field?.state === "unknown") return "Не знает";
  if (field?.state === "negative") return "Нет";
  return "Не заполнено";
}

function changedIntakeFields(before: IntakeCard | null, after: IntakeCard): string[] {
  const fields: Array<[string, IntakeField]> = [
    ...Object.entries(after).filter((entry): entry is [string, IntakeField] => typeof entry[1] === "object" && entry[1] !== null && "state" in entry[1]),
    ...Object.entries(after.address).map(([key, value]): [string, IntakeField] => [`address.${key}`, value]),
  ];
  return fields.filter(([path, value]) => {
    const previous = before ? path.startsWith("address.") ? before.address[path.slice(8)] : (before as unknown as Record<string, IntakeField>)[path] : undefined;
    return JSON.stringify(previous) !== JSON.stringify(value) && (before !== null || value.state !== "unanswered");
  }).map(([path, value]) => `${path} → ${intakeFieldText(value)}`);
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

type DDSReviewAction = { action_id: string; type: string; accepted: boolean; server_at: string; payload?: { status?: components["schemas"]["ReactionStatus"]; comment?: string } };

// DDSStatusHistory is the dispatcher's saved reaction statuses with their
// comments, in journal order (ADR-030) — what the trainee's pencil wrote
// into the card.
function DDSStatusHistory({ actions }: { actions: DDSReviewAction[] }) {
  const saved = actions.filter((action) => action.accepted && action.type === "set_status");
  if (saved.length === 0) return <p>Статусы реагирования не проставлены.</p>;
  return <>
    <h2>Статусы реагирования</h2>
    <ol>{saved.map((action) => <li key={action.action_id}>{formatDateTime(action.server_at)} · <strong>{reactionLabel(action.payload?.status)}</strong>{action.payload?.comment ? ` — ${action.payload.comment}` : ""}</li>)}</ol>
  </>;
}

// DDSCommsReview is ADR-031's communication log for the instructor: every
// delivered crew report or incoming call with its text, whether an
// incoming call was answered or missed, the first saved status after it
// reached the trainee, and the trainee's own outgoing calls — in time
// order. It reads the instructor's item view (texts are never hidden
// here); the rubric judges the delays, this only shows them.
function DDSCommsReview({ item }: { item: Item }) {
  const card = item.card as { contacts?: { key: string; label: string }[] };
  const label = (key?: string | null) => card.contacts?.find((contact) => contact.key === key)?.label ?? key ?? "";
  const statuses = item.actions.filter((action) => action.accepted && action.type === "set_status");
  const firstStatusAfter = (at: string) => statuses.find((action) => new Date(action.server_at) >= new Date(at));
  const gap = (from: string, to: string) => Math.round((new Date(to).getTime() - new Date(from).getTime()) / 1_000);
  type Row = { key: string; at: string; text: ReactNode };
  const rows: Row[] = [];
  for (const event of item.events) {
    if (event.delivery !== "notice" && event.delivery !== "phone_incoming") continue;
    const answer = event.delivery === "phone_incoming" ? item.calls.find((call) => call.event_key === event.key) : undefined;
    const start = event.delivery === "phone_incoming" ? answer?.started_at : event.delivered_at;
    const reaction = start ? firstStatusAfter(start) : undefined;
    const payload = (reaction?.payload ?? {}) as { status?: components["schemas"]["ReactionStatus"] };
    rows.push({ key: `e-${event.key}`, at: event.delivered_at, text: <>
      <strong>{event.delivery === "phone_incoming" ? "Входящий звонок" : "Доклад"}</strong> · {label(event.from)} · {formatDateTime(event.delivered_at)}{event.late ? " (с опозданием)" : ""}
      {event.delivery === "phone_incoming" && (answer ? ` · принят через ${gap(event.delivered_at, answer.started_at)} с` : <span className="monitor-comms-alarm"> · не принят</span>)}
      {" · "}{reaction ? `реакция: ${reactionLabel(payload.status)} через ${gap(start!, reaction.server_at)} с` : "реакции (статуса) нет"}
      <br />«{event.text}»
    </> });
  }
  for (const call of item.calls) {
    if (call.direction === "incoming") continue;
    rows.push({ key: `c-${call.id}`, at: call.started_at, text: <><strong>Исходящий звонок</strong> · {label(call.contact_key)} · {formatDateTime(call.started_at)}{call.ended_at ? "" : " · не завершён"}</> });
  }
  if (rows.length === 0) return null;
  rows.sort((a, b) => new Date(a.at).getTime() - new Date(b.at).getTime());
  return <>
    <h2>Связь с бригадой</h2>
    <ol>{rows.map((row) => <li key={row.key}>{row.text}</li>)}</ol>
  </>;
}
