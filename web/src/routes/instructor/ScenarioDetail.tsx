import { Link, useParams } from "react-router-dom";
import {
  useScenario,
  useScenarioPreview,
  useScenarioVersions,
  type CardPreview,
  type ScenarioReference,
  type ScenarioVersionSummary,
} from "../../api/content";
import { errorMessage } from "../../api/errors";
import { IncidentCard } from "../../components/IncidentCard";
import { formatDateTime } from "../../format";
import { reactionLabel, scenarioStatusLabel, versionStatusLabel } from "../../labels";

// GET /scenarios/{id} + /preview + /versions (slice-2-plan.md's C6): the
// scenario's current approved content as an instructor reviews it before
// assigning it — IncidentCard is the same allowlist view a trainee will
// see (slice 3), the blocks below it are instructor-only (the эталон is
// never sent to a trainee — internal/content's Reference doc comment).
// Deliberately no edit/approve controls and no raw-JSON view: until
// slice 11 scenarios are prepared, reviewed and loaded outside the UI.
export function ScenarioDetailRoute() {
  const { scenarioId = "" } = useParams();
  const scenario = useScenario(scenarioId);
  const preview = useScenarioPreview(scenarioId);

  if (scenario.isPending || preview.isPending) {
    return <p>Загрузка…</p>;
  }
  if (scenario.isError) {
    return <p className="error">{errorMessage(scenario.error)}</p>;
  }
  if (preview.isError) {
    return <p className="error">{errorMessage(preview.error)}</p>;
  }

  const s = scenario.data;
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
