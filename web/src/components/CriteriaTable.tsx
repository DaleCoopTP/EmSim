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
      <td>{explanationRu(criterion.explanation) || "—"}
        <DetailList criterion={criterion} />
      </td>
    </tr>)}
  </tbody></table>;
}

const reactionRu: Record<string, string> = {
  added: "Добавлена", received: "Получена", accepted: "Принята", not_accepted: "Не принята", responding: "Начало реагирования",
  arrived: "Прибытие", working: "Проведение работ", completed: "Работы завершены", refused: "Отказ от выполнения работ",
  completed_without_team: "Завершение работ без бригады",
};

// Assessments recorded before 2026-09-29 carry the DDS rules' English
// explanations; they are shown in the Russian wording the server uses now.
const legacyExplanations: [RegExp, (m: RegExpMatchArray) => string][] = [
  [/^(\d+)s within the (\d+)s norm$/, (m) => `${m[1]} с — в пределах норматива ${m[2]} с`],
  [/^(\d+)s exceeds the (\d+)s norm but within the (\d+)s partial allowance$/, (m) => `${m[1]} с — больше норматива ${m[2]} с, но в пределах допуска ${m[3]} с`],
  [/^(\d+)s exceeds even the (\d+)s partial allowance$/, (m) => `${m[1]} с — больше даже допуска ${m[2]} с`],
  [/^decision "(\w+)" matches the reference$/, (m) => `решение «${reactionRu[m[1]] ?? m[1]}» совпадает с эталоном`],
  [/^decision "(\w+)" does not match the reference "(\w+)"$/, (m) => `решение «${reactionRu[m[1]] ?? m[1]}» не совпадает с эталоном «${reactionRu[m[2]] ?? m[2]}»`],
  [/^(\d+)\/(\d+) crew reports got a timely status$/, (m) => `на ${m[1]} из ${m[2]} докладов бригады статус поставлен вовремя`],
  [/^no crew report got a timely status$/, () => "ни на один доклад бригады статус не поставлен вовремя"],
  [/^expected chain observed in order, none earlier than its own crew report$/, () => "ожидаемая цепочка статусов соблюдена, ни один статус не поставлен раньше доклада"],
  [/^(\d+)\/(\d+) expected transitions observed in order$/, (m) => `по порядку выполнено ${m[1]} из ${m[2]} ожидаемых переходов`],
  [/^none of the expected transitions were observed in the right order$/, () => "ни один ожидаемый переход статуса не выполнен в нужном порядке"],
  [/^(\d+)\/(\d+) required calls completed$/, (m) => `выполнено ${m[1]} из ${m[2]} обязательных звонков`],
  [/^no required call was completed$/, () => "ни один обязательный звонок не выполнен"],
  [/^not applicable to this case$/, () => "не применимо к этому случаю"],
  [/^the trainee never reached this milestone$/, () => "обучаемый не дошёл до этого этапа"],
  [/^no primary decision was ever made$/, () => "первичное решение не принято"],
  [/^a comment was recorded$/, () => "комментарий есть"],
  [/^comment_required but no comment was recorded$/, () => "комментарий обязателен, но не написан"],
  [/^the required call was completed$/, () => "обязательный звонок выполнен"],
  [/^no completed call to the required contact$/, () => "нет завершённого звонка нужному абоненту"],
];

function explanationRu(text: string | undefined): string {
  if (!text) return "";
  for (const [pattern, render] of legacyExplanations) {
    const match = text.match(pattern);
    if (match) return render(match);
  }
  return text;
}
