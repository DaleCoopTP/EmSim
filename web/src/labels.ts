// Russian display labels for closed enums shared by IncidentCard and the
// instructor scenario detail route (ScenarioReference/CardPreview both
// carry ReactionStatus and the applicant status enum) — kept separate
// from either so neither has to import from the other.
import type { components } from "./api/schema";

type ReactionStatus = components["schemas"]["ReactionStatus"];

// Names as in the DDS guide («Памятка для ДДС», стр. 21–23, ADR-030).
const reactionLabels: Record<ReactionStatus, string> = {
  added: "Добавлена",
  received: "Получена",
  accepted: "Принята",
  not_accepted: "Не принята",
  responding: "Начало реагирования",
  arrived: "Прибытие",
  working: "Проведение работ",
  completed: "Работы завершены",
  refused: "Отказ от выполнения работ",
  completed_without_team: "Завершение работ без бригады",
};

export function reactionLabel(status: ReactionStatus | undefined): string {
  return status ? (reactionLabels[status] ?? status) : "—";
}

type CardStatus = NonNullable<components["schemas"]["ItemSummary"]["card_status"]>;

// Derived DDS card status (ADR-030, памятка стр. 27–28).
const cardStatusLabels: Record<CardStatus, string> = {
  registered: "Зарегистрирована",
  not_notified: "Не оповещено",
  in_progress: "В работе",
  refused: "Отказ",
  completed: "Завершена",
  not_completed: "Не завершено",
};

export function cardStatusLabel(status: CardStatus | undefined): string {
  return status ? (cardStatusLabels[status] ?? status) : "—";
}

// «Не оповещено», «Отказ» and «Не завершено» are red in the real ARM-112
// card list — they are what the 112 control department reviews.
export function cardStatusAlarm(status: CardStatus | undefined): boolean {
  return status === "not_notified" || status === "refused" || status === "not_completed";
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
