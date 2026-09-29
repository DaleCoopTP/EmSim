import { useMaintenanceOn } from "../../api/system";
import { Link, useNavigate, useOutletContext, useParams } from "react-router-dom";
import {
  useApproveScenario,
  useCreateScenario,
  useIntake112Catalog,
  useScenario,
  useScenarioPreview,
  useScenarioVersions,
  useStartPreviewRun,
  type CardPreview,
  type Intake112Fact,
  type Scenario,
  type ScenarioReference,
  type ScenarioVersionSummary,
} from "../../api/content";
import { errorMessage } from "../../api/errors";
import type { Me } from "../../api/useMe";
import { IncidentCard } from "../../components/IncidentCard";
import { IntakeReferenceCard, type IntakeReferenceLike } from "../../components/IntakeReferenceCard";
import { formatDateTime } from "../../format";
import { reactionLabel, scenarioStatusLabel, versionStatusLabel } from "../../labels";

// GET /scenarios/{id} + /preview + /versions (slice-2-plan.md's C6): the
// scenario's current approved content as an instructor reviews it before
// assigning it — IncidentCard is the same allowlist view a trainee will
// see (slice 3), the blocks below it are instructor-only (the эталон is
// never sent to a trainee — internal/content's Reference doc comment).
// The operator112 branch (112-7/ADR-027) reads straight from GET
// /scenarios/{id}'s own body instead of the separate /preview endpoint:
// unlike the DDS branch below, an operator112 scenario the caller owns
// may still be an unapproved draft, and /preview only ever resolves an
// *approved* version (404 otherwise) — this route must still render a
// draft the caller's own GET /scenarios/{id} (owner-only fallback,
// internal/content/http's scenarioDetail) already returned successfully.
export function ScenarioDetailRoute() {
  const { scenarioId = "" } = useParams();
  const me = useOutletContext<Me>();
  const navigate = useNavigate();
  const scenario = useScenario(scenarioId);
  const preview = useScenarioPreview(scenarioId);

  if (scenario.isPending) {
    return <p>Загрузка…</p>;
  }
  if (scenario.isError) {
    return <p className="error">{errorMessage(scenario.error)}</p>;
  }

  const s = scenario.data;
  if (s.exercise_type === "operator112_intake") {
    return <Operator112ScenarioDetail s={s} me={me} onNavigate={navigate} />;
  }

  if (preview.isPending) {
    return <p>Загрузка…</p>;
  }
  if (preview.isError) {
    return <p className="error">{errorMessage(preview.error)}</p>;
  }
  if ("intake112" in preview.data) {
    // Unreachable in practice (s.exercise_type already routed operator112
    // above), kept only so TypeScript can narrow preview.data below.
    return null;
  }
  const { card, reference } = preview.data;

  return (
    <section className="instructor-page scenario-detail">
      <p className="back-link">
        <Link to="/instructor/scenarios">← К каталогу</Link>
      </p>
      <header className="page-heading scenario-heading">
        <div><h1>{s.title}</h1><p>{s.target_service} · версия {s.version}</p></div>
        <span className={`status-badge status-${s.status}`}>{scenarioStatusLabel(s.status)}</span>
      </header>
      <dl className="scenario-meta">
        <dt>Служба</dt><dd>{s.target_service}</dd>
        <dt>Сложность</dt><dd>{s.difficulty}</dd>
        <dt>Версия</dt><dd>{s.version}</dd>
        {s.source_key && <><dt>Ключ файла</dt><dd>{s.source_key}</dd></>}
      </dl>

      <div className="scenario-review-grid">
        <IncidentCard card={card} />
        <ReferenceBlock reference={reference} card={card} />
      </div>
      <div className="scenario-notes-grid">
        <EventsBlock hasEvents={s.has_events ?? false} />
        <CallBlock reference={reference} card={card} />
      </div>
      <VersionsBlock scenarioId={scenarioId} />
    </section>
  );
}

// The wire's own ScenarioBody is intentionally loose ({exercise_type} &
// {[key: string]: unknown}) — this narrows it to just what this
// read-only detail view displays, across every intake112 shape
// (prepared/free_text, incoming_call/card_only/full_case), not only the
// editor's own full_case+free_text one (Intake112EditorBody).
type Intake112DisplayBody = {
  exercise_type: "operator112_intake";
  intake112: {
    mode?: "incoming_call" | "card_only" | "full_case";
    caller_mode?: "prepared" | "free_text";
    call?: { aon: string; local_time: string; script?: string[] };
    dialogue?: { facts: Intake112Fact[]; caller?: { persona: string; opening: { text: string } } };
    reference: {
      expected_types?: string[];
      case_description?: string;
      expected_services?: string[];
      expected_card?: IntakeReferenceLike["expected_card"];
      expected_profiles?: IntakeReferenceLike["expected_profiles"];
      recipient_service?: string;
      // ADR-028 (operator112/rubric-v3's DESCRIPTION_CONTENT).
      description_questions?: { id: string; question: string }[];
    };
  };
};

// operator112EditorEligible mirrors internal/content/editor.go's own
// function of the same name: only full_case+free_text scenarios can be
// edited/copied/previewed through the 112-7 editor. A prepared-dialogue
// or card_only/incoming_call scenario (still only ever created by file
// import — slice-112-7-plan.md's own scope decision 4) is shown
// read-only here, same as before this change.
function operator112EditorEligible(intake: Intake112DisplayBody["intake112"]): boolean {
  return intake.mode === "full_case" && intake.caller_mode === "free_text";
}

function Operator112ScenarioDetail({ s, me, onNavigate }: { s: Scenario; me: Me; onNavigate: (path: string, options?: { replace?: boolean }) => void }) {
  const scenarioId = s.id;
  const intake = (s.body as unknown as Intake112DisplayBody).intake112;
  const eligible = operator112EditorEligible(intake);
  const isOwner = s.created_by === me.user.id;
  const errorCount = (s.issues ?? []).filter((issue) => issue.severity === "error").length;
  const hasDraft = s.version_status === "draft" || s.version_status === "superseded";
  const canPreviewOrApprove = isOwner && eligible && (s.version_status === "approved" || (hasDraft && errorCount === 0));

  const createMutation = useCreateScenario();
  const approveMutation = useApproveScenario();
  const startPreview = useStartPreviewRun();
  const maintenance = useMaintenanceOn();
  const catalog = useIntake112Catalog();

  const copy = () => {
    createMutation.mutate(
      { title: `${s.title} (копия)`, difficulty: s.difficulty, copyFromVersionId: s.version_id },
      { onSuccess: (created) => onNavigate(`/instructor/scenarios/${created.id}/edit`) },
    );
  };
  const approve = () => {
    if (!s.version_id || !s.digest) return;
    approveMutation.mutate({ scenarioId, versionId: s.version_id, baseDigest: s.digest });
  };
  const preview = () => {
    if (!s.version_id) return;
    startPreview.mutate(
      { scenarioId, versionId: s.version_id },
      { onSuccess: (result) => onNavigate(`/instructor/preview/${result.item_id}?lesson=${encodeURIComponent(result.lesson_id)}`) },
    );
  };

  return (
    <section className="instructor-page scenario-detail">
      <p className="back-link"><Link to="/instructor/scenarios">← К сценариям</Link></p>
      <header className="page-heading scenario-heading">
        <div><h1>{s.title}</h1><p>Оператор 112 · версия {s.version}{intake.mode ? ` · ${intake.mode}` : ""}{intake.caller_mode ? ` · ${intake.caller_mode}` : ""}</p></div>
        <span className={`status-badge status-${s.version_status ?? s.status}`}>{versionStatusLabel(s.version_status) !== "—" ? versionStatusLabel(s.version_status) : scenarioStatusLabel(s.status)}</span>
      </header>

      {!eligible && (
        <p className="notice">Этот сценарий использует подготовленный диалог или карточку без разговора — редактор 112-7 поддерживает только «полный кейс» с ИИ-заявителем; такие сценарии по-прежнему готовятся файловым импортом.</p>
      )}
      {isOwner && errorCount > 0 && (
        <p className="error">Черновик содержит {errorCount} блокирующих ошибок проверки — откройте редактор, чтобы их устранить.</p>
      )}

      <section className="scenario-reference-pane scenario-reference-card">
        <h2>Эталон: карточка после обработки вызова</h2>
        <IntakeReferenceCard reference={intake.reference} catalog={catalog.data} aon={intake.call?.aon} localTime={intake.call?.local_time}
          facts={intake.dialogue?.facts} withCall={intake.mode !== "card_only"} />
      </section>

      <div className="scenario-review-grid">
        <section className="scenario-reference-pane">
          <h2>Ситуация для преподавателя</h2>
          <dl>
            {intake.reference.case_description && <><dt>Описание ситуации</dt><dd>{intake.reference.case_description}</dd></>}
            {intake.reference.recipient_service && <><dt>Адресат</dt><dd>{intake.reference.recipient_service}</dd></>}
            {!(intake.reference.description_questions && intake.reference.description_questions.length > 0) &&
              <><dt>Вопросы к описанию (ADR-028)</dt><dd>Не заданы — блок DESCRIPTION_CONTENT/DESCRIPTION_PRESENT получит 0.</dd></>}
            {!intake.reference.expected_card && <><dt>Эталон карточки</dt><dd>Не задан — блок адреса получит 0.</dd></>}
            {!intake.reference.expected_profiles && <><dt>Эталон профильных карт</dt><dd>Не задан — блок карт получит 0.</dd></>}
          </dl>
        </section>
        {intake.call?.script && (
          <section className="scenario-reference-pane">
            <h2>Подготовленный вызов</h2>
            <ol>{intake.call.script.map((line, index) => <li key={index}>{line}</li>)}</ol>
          </section>
        )}
      </div>

      {intake.dialogue && intake.dialogue.facts.length > 0 && <CallerKnowledge dialogue={intake.dialogue} />}

      <div className="scenario-editor-actions">
        {eligible && <button type="button" disabled={createMutation.isPending} onClick={copy}>Копировать в свой черновик</button>}
        {isOwner && eligible && <Link to={`/instructor/scenarios/${scenarioId}/edit`} className="arm-primary-action">Редактировать</Link>}
        {isOwner && eligible && <button type="button" disabled={!canPreviewOrApprove || startPreview.isPending || maintenance.on} title={maintenance.on ? "Идут технические работы: предпросмотр недоступен" : undefined} onClick={preview}>Пройти самому (предпросмотр)</button>}
        {isOwner && eligible && hasDraft && <button type="button" disabled={!canPreviewOrApprove || approveMutation.isPending} onClick={approve}>Утвердить</button>}
        {createMutation.isError && <p className="error">{errorMessage(createMutation.error)}</p>}
        {startPreview.isError && <p className="error">{errorMessage(startPreview.error)}</p>}
        {approveMutation.isError && <p className="error">{errorMessage(approveMutation.error)}</p>}
        {approveMutation.isSuccess && <p>Сценарий утверждён.</p>}
      </div>

      <VersionsBlock scenarioId={scenarioId} />
    </section>
  );
}

const knowledgeLabels: Record<string, string> = { initial: "сообщает сразу", on_question: "на вопрос", unknown: "не знает" };

// CallerKnowledge is the AI caller's own brief: persona, opening line and
// what the caller knows. A fact with a card_path is already drawn in the
// reference card above, so only the rest is listed — what the caller can
// tell beyond the card, and what they do not know at all.
function CallerKnowledge({ dialogue }: { dialogue: NonNullable<Intake112DisplayBody["intake112"]["dialogue"]> }) {
  const extra = dialogue.facts.filter((fact) => !fact.card_path || fact.knowledge === "unknown");
  return (
    <details className="scenario-reference-pane caller-knowledge">
      <summary>Что знает заявитель{extra.length ? ` · сведений вне карточки: ${extra.length}` : ""}</summary>
      {dialogue.caller && <p>{dialogue.caller.persona}</p>}
      {dialogue.caller && <p>Первая фраза: «{dialogue.caller.opening.text}»</p>}
      {extra.length > 0 ? <table>
        <thead><tr><th>Сведение</th><th>Когда сообщает</th><th>Что говорит</th></tr></thead>
        <tbody>{extra.map((fact) => (
          <tr key={fact.id}><td>{fact.label || fact.id}</td><td>{knowledgeLabels[fact.knowledge] ?? fact.knowledge}</td><td>{fact.knowledge === "unknown" ? "—" : fact.value ?? "—"}</td></tr>
        ))}</tbody>
      </table> : <p>Всё, что знает заявитель, уже отражено в эталонной карточке.</p>}
    </details>
  );
}

const decisionLabels: Record<string, string> = {
  accepted: "Принять",
  not_accepted: "Отклонить",
  completed_without_team: "Завершить без бригады",
};

// field_corrections.path is restricted by scenario.schema.json to a
// single allowlisted JSON pointer today; the label map only needs that
// one entry, kept as a map (not a literal string) so a future path is a
// one-line addition here, not a rewrite of the renderer.
const fieldPathLabels: Record<string, string> = {
  "/card/address/okrug": "Округ",
};

function currentFieldValue(card: CardPreview, path: string): string | undefined {
  if (path === "/card/address/okrug") return card.address.okrug;
  return undefined;
}

function ReferenceBlock({ reference, card }: { reference: ScenarioReference; card: CardPreview }) {
  const corrections = reference.field_corrections ?? [];
  return (
    <section className="scenario-reference-pane">
      <h2>Эталон</h2>
      <dl>
        <dt>Первичное решение</dt>
        <dd>{decisionLabels[reference.primary_decision.status ?? ""] ?? reactionLabel(reference.primary_decision.status)}</dd>
        {reference.primary_decision.comment_required && (
          <>
            <dt>Комментарий</dt>
            <dd>Обязателен</dd>
          </>
        )}
        {reference.expected_chain.length > 0 && (
          <>
            <dt>Ожидаемая цепочка статусов</dt>
            <dd>{reference.expected_chain.map(reactionLabel).join(" → ")}</dd>
          </>
        )}
      </dl>

      {corrections.length > 0 && (
        <>
          <h3>Исправления</h3>
          <ul>
            {corrections.map((c, i) => {
              const label = (c.path && fieldPathLabels[c.path]) ?? c.path ?? "Поле";
              const current = c.path ? currentFieldValue(card, c.path) : undefined;
              return (
                <li key={i}>
                  {label}: {current ? `«${current}» → ` : ""}
                  «{c.expected_value}»; до статуса «{reactionLabel(c.before_status)}»
                </li>
              );
            })}
          </ul>
        </>
      )}

      {reference.pilot_goal === "accept_card" && (
        <p className="notice">Пилот: цель — принятие карточки; полный порядок обработки уточняется.</p>
      )}
      {reference.guide_refs && reference.guide_refs.length > 0 && <p>Памятка: {reference.guide_refs.join("; ")}</p>}
      {reference.notes && <p>{reference.notes}</p>}
    </section>
  );
}

function EventsBlock({ hasEvents }: { hasEvents: boolean }) {
  return (
    <section className="scenario-note-panel">
      <h2>События</h2>
      <p>{hasEvents ? "У сценария есть события по таймлайну." : "Событий нет."}</p>
    </section>
  );
}

function CallBlock({ reference, card }: { reference: ScenarioReference; card: CardPreview }) {
  if (!reference.call.required) {
    return (
      <section className="scenario-note-panel">
        <h2>Звонки</h2>
        <p>Не требуются.</p>
      </section>
    );
  }
  const target = card.contacts?.find((c) => c.key === reference.call.to);
  return (
    <section className="scenario-note-panel">
      <h2>Звонки</h2>
      <p>
        Требуется до статуса «{reactionLabel(reference.call.before_status)}»
        {target ? `, контакт: ${target.label} (${target.number})` : reference.call.to ? `, контакт: ${reference.call.to}` : ""}
      </p>
      {reference.call.must_mention && reference.call.must_mention.length > 0 && (
        <p>Обязательно упомянуть: {reference.call.must_mention.join(", ")}</p>
      )}
    </section>
  );
}

function VersionsBlock({ scenarioId }: { scenarioId: string }) {
  const versions = useScenarioVersions(scenarioId);
  return (
    <section className="scenario-versions">
      <h2>Версии</h2>
      {versions.isPending && <p>Загрузка…</p>}
      {versions.isError && <p className="error">{errorMessage(versions.error)}</p>}
      {versions.data && (
        <table>
          <thead>
            <tr>
              <th>Версия</th>
              <th>Статус</th>
              <th>Сложность</th>
              <th>Утверждена</th>
              <th>Digest</th>
            </tr>
          </thead>
          <tbody>
            {versions.data.map((v) => (
              <tr key={v.id}>
                <td>{v.version}</td>
                <td>{versionStatusLabel(v.status)}</td>
                <td>{v.difficulty}</td>
                <td>{formatDateTime(v.approved_at)}</td>
                <td title={v.digest}>{shortDigest(v.digest)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}

function shortDigest(digest: ScenarioVersionSummary["digest"]): string {
  return digest ? `${digest.slice(0, 12)}…` : "—";
}
