import type { ReactNode } from "react";
import type { CriterionResult } from "../api/assessment";
import type { components } from "../api/schema";
import { criterionStatusLabels, round2, type RubricEffectiveCriterion } from "./IntakeAutoAssessment";

type CriterionDetail = components["schemas"]["CriterionDetail"];
type GrammarError = components["schemas"]["GrammarError"];

const grammarKindLabels: Record<GrammarError["kind"], string> = {
  spelling: "орфография",
  grammar: "грамматика",
  punctuation: "пунктуация",
};

// highlight wraps the first (case-insensitive) occurrence of each error
// fragment in <mark>. The judge's fragments are checked against the
// comment's own text server-side (ADR-034), so a fragment that is not
// found here can only differ in whitespace — it is then listed under the
// text without a highlight rather than dropped.
function highlight(text: string, fragments: string[]): ReactNode[] {
  const ranges: { start: number; end: number }[] = [];
  const lower = text.toLowerCase();
  for (const fragment of fragments) {
    const at = lower.indexOf(fragment.toLowerCase());
    if (at < 0 || fragment === "") continue;
    if (ranges.some((r) => at < r.end && at + fragment.length > r.start)) continue;
    ranges.push({ start: at, end: at + fragment.length });
  }
  ranges.sort((a, b) => a.start - b.start);
  const out: ReactNode[] = [];
  let cursor = 0;
  ranges.forEach((r, i) => {
    if (r.start > cursor) out.push(text.slice(cursor, r.start));
    out.push(<mark key={i}>{text.slice(r.start, r.end)}</mark>);
    cursor = r.end;
  });
  if (cursor < text.length) out.push(text.slice(cursor));
  return out;
}

// CommentQuestionRow is one D_COMMENT_CONTENT question (ADR-034): which
// status comment it was about, what the reference expected, what the
// trainee wrote, and the judge's verdict with its share of the points.
function CommentQuestionRow({ detail }: { detail: CriterionDetail }) {
  return <li>
    <strong>{detail.label ?? detail.key}</strong>: {criterionStatusLabels[detail.status] ?? detail.status} — {round2(detail.points ?? 0)}/{round2(detail.max_points ?? 0)}
    {detail.expected ? <div>Ожидалось: {detail.expected}</div> : null}
    <div>Комментарий: {detail.actual ? <q>{detail.actual}</q> : <em>нет комментария</em>}</div>
  </li>;
}

// GrammarRow is one G_GRAMMAR comment (ADR-034): its text with the found
// fragments highlighted and each fix listed.
function GrammarRow({ detail }: { detail: CriterionDetail }) {
  const errors = detail.errors ?? [];
  return <li>
    <strong>{detail.label ?? detail.key}</strong>: {errors.length === 0 ? "ошибок нет" : `ошибок: ${errors.length}`}
    <div>{highlight(detail.actual ?? "", errors.map((e) => e.fragment))}</div>
    {errors.length > 0 ? <ul>{errors.map((e, i) => <li key={i}>«{e.fragment}» → «{e.correction}» ({grammarKindLabels[e.kind] ?? e.kind})</li>)}</ul> : null}
  </li>;
}

function DetailList({ criterion }: { criterion: CriterionResult }) {
  const details = criterion.details ?? [];
  if (details.length === 0) return null;
  const grammar = criterion.id === "G_GRAMMAR";
  return <details><summary>Подробности ({details.length})</summary>
    <ul>{details.map((d) => grammar ? <GrammarRow key={d.key} detail={d} /> : <CommentQuestionRow key={d.key} detail={d} />)}</ul>
  </details>;
}

// CriteriaTable is the DDS instructor review's own auto-assessment table.
// The "Критерий" column shows rubric_effective's title for the
// criterion's id (ДДС-3: dds/rubric-v1, v2 and v3 share the review UI,
// so T_PROGRESS/C_CALLS — and v1's own C_CALL_MADE/G_ADDRESS — read as
// their own titles, not bare ids), the bare id itself only as a
// fallback when rubric_effective has none. ДДС-4/ADR-034: a judged
// criterion (D_COMMENT_CONTENT, G_GRAMMAR) expands into its per-question
// / per-comment rows.
export function CriteriaTable({ criteria, rubricByID }: { criteria: CriterionResult[]; rubricByID: Record<string, RubricEffectiveCriterion> }) {
  if (criteria.length === 0) return <p>Автооценка ещё не готова.</p>;
  return <table><thead><tr><th>Критерий</th><th>Статус</th><th>Основание</th></tr></thead><tbody>
    {criteria.map((criterion) => <tr key={criterion.id}>
      <td>{rubricByID[criterion.id]?.title ?? criterion.id}{criterion.critical ? " · критичный" : ""}</td>
      <td>{criterionStatusLabels[criterion.status] ?? criterion.status}</td>
      <td>{criterion.explanation || "—"}{criterion.evidence_refs?.length ? ` (${criterion.evidence_refs.join(", ")})` : ""}
        <DetailList criterion={criterion} />
      </td>
    </tr>)}
  </tbody></table>;
}
