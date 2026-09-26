import type { CriterionResult } from "../api/assessment";
import type { components } from "../api/schema";

type CriterionDetail = components["schemas"]["CriterionDetail"];
export type RubricEffectiveCriterion = { id: string; title?: string; kind?: string; disabled?: boolean; weight?: number; critical?: boolean };

export const criterionStatusLabels: Record<string, string> = {
  met: "выполнено",
  partial: "частично",
  not_met: "не выполнено",
  not_applicable: "не применимо",
  unavailable: "не проверено",
};

// 112-6/ADR-026: operator112/rubric-v2's criteria are split into scored
// blocks (kind=deterministic, a 0..1 fraction of their own weight) and
// penalties (kind=penalty, a flat points deduction).
export function isPenaltyCriterion(rubricCriteria: Record<string, RubricEffectiveCriterion>, id: string): boolean {
  return rubricCriteria[id]?.kind === "penalty";
}

export function round2(value: number): number {
  return Math.round(value * 100) / 100;
}

// IntakeAutoAssessment is operator112/rubric-v2's own read-only auto-
// assessment display (112-6/ADR-026, extracted for 112-7/ADR-027 so the
// instructor's item review and the scenario editor's own preview screen
// share one component instead of two copies drifting apart): blocks
// (their own points out of weight, plus an expandable per-field/per-card
// Details breakdown) and penalties (points charged, plus which field/
// service/card triggered them) shown as two separate tables, since they
// are scored — and charged — by entirely different rules.
export function IntakeAutoAssessment({ criteria, rubricCriteria }: { criteria: CriterionResult[]; rubricCriteria: Record<string, RubricEffectiveCriterion> }) {
  if (criteria.length === 0) return <p>Автооценка ещё не готова.</p>;
  const blocks = criteria.filter((c) => !isPenaltyCriterion(rubricCriteria, c.id));
  const penalties = criteria.filter((c) => isPenaltyCriterion(rubricCriteria, c.id));
  const detailText = (d: CriterionDetail) => {
    const parts = [d.actual ? `заполнено: ${d.actual}` : null, d.expected ? `ожидалось: ${d.expected}` : null];
    const suffix = parts.filter(Boolean).join(", ");
    return `${d.label ?? d.key}: ${criterionStatusLabels[d.status] ?? d.status}${suffix ? ` (${suffix})` : ""} — ${d.points ?? 0}/${d.max_points ?? 0}`;
  };
  return <>
    <h3>Блоки</h3>
    <table><thead><tr><th>Блок</th><th>Баллы</th><th>Статус</th><th>Основание</th></tr></thead><tbody>
      {blocks.map((c) => {
        const weight = rubricCriteria[c.id]?.weight ?? c.weight;
        const points = c.score != null ? round2(c.score * weight) : null;
        return <tr key={c.id}>
          <td>{rubricCriteria[c.id]?.title ?? c.id}{c.critical ? " · критичный" : ""}</td>
          <td>{points == null ? "—" : `${points} из ${weight}`}</td>
          <td>{criterionStatusLabels[c.status] ?? c.status}</td>
          <td>{c.explanation || "—"}
            {c.details?.length ? <details><summary>Подробности ({c.details.length})</summary><ul>{c.details.map((d) => <li key={d.key}>{detailText(d)}</li>)}</ul></details> : null}
          </td>
        </tr>;
      })}
    </tbody></table>
    <h3>Штрафы</h3>
    <table><thead><tr><th>Штраф</th><th>Баллы</th><th>Основание</th></tr></thead><tbody>
      {penalties.map((c) => <tr key={c.id}>
        <td>{rubricCriteria[c.id]?.title ?? c.id}</td>
        <td>{c.status === "not_applicable" ? "—" : `−${c.penalty_points ?? 0}`}</td>
        <td>{c.explanation || "—"}
          {c.details?.length ? <details><summary>Подробности ({c.details.length})</summary><ul>{c.details.map((d) => <li key={d.key}>{d.label ?? d.key}</li>)}</ul></details> : null}
        </td>
      </tr>)}
    </tbody></table>
  </>;
}
