// Russian display labels for closed enums shared by IncidentCard and the
// instructor scenario detail route (ScenarioReference/CardPreview both
// carry ReactionStatus and the applicant status enum) — kept separate
// from either so neither has to import from the other.
import type { components } from "./api/schema";

type ReactionStatus = components["schemas"]["ReactionStatus"];

const reactionLabels: Record<ReactionStatus, string> = {
  added: "Добавлена",
  received: "Получена",
  accepted: "Принята",
  not_accepted: "Отклонена",
  responding: "Выехала",
  arrived: "Прибыла",
  working: "Работает",
  completed: "Завершена",
  refused: "Отказ",
  completed_without_team: "Завершена без бригады",
};

export function reactionLabel(status: ReactionStatus | undefined): string {
  return status ? (reactionLabels[status] ?? status) : "—";
}

const applicantStatusLabels: Record<string, string> = {
  witness: "Очевидец",
  victim: "Пострадавший",
  relative: "Родственник",
  acquaintance: "Знакомый",
  child: "Ребёнок",
  participant: "Участник",
};

export function applicantStatusLabel(status: string | undefined): string | undefined {
  return status ? (applicantStatusLabels[status] ?? status) : undefined;
}

const scenarioStatusLabels: Record<string, string> = {
  draft: "черновик",
  approved: "утверждён",
  archived: "архив",
};

export function scenarioStatusLabel(status: string): string {
  return scenarioStatusLabels[status] ?? status;
}

const versionStatusLabels: Record<string, string> = {
  draft: "черновик",
  approved: "утверждена",
  superseded: "заменена",
};

export function versionStatusLabel(status: string | undefined): string {
  return status ? (versionStatusLabels[status] ?? status) : "—";
}
