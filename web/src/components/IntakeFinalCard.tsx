import type { IntakeCatalog, IntakeProfileAnswer } from "../routes/trainee/Operator112Workplace";
import { armCardNumber } from "../arm112Number";
import { applicantStatusLabel } from "../labels";
import { Arm112CardView, type CardValue } from "./Arm112CardView";

type Field = { state: string; value?: string };
// The final card as the review reads it from evidence (final_card or the
// notification snapshot); loosely typed like ItemReview's own IntakeCard.
export type FinalCardLike = {
  number: string; aon: string; call_local_time: string;
  applicant_name: Field; applicant_status: Field; age: Field; address: Record<string, Field>;
  incident_types?: string[]; profiles?: Record<string, { answers: Record<string, { state: string; value?: string; values?: string[] }> }>;
  complaint: Field; victims_present: Field; victims_count: Field; provided_phone: Field; on_site_phone?: Field; channel?: Field;
  foreign_language?: Field; no_on_site?: Field; no_access?: Field;
};

const value = (field: Field | undefined): CardValue | undefined => {
  if (field?.state === "known" && field.value) return { text: field.value };
  if (field?.state === "unknown") return { text: "неизвестно", unknown: true };
  return undefined;
};
const flag = (field: Field | undefined) => field?.state === "known";

// IntakeFinalCard shows the card the trainee ended up with — the same ARM-112
// layout as their workplace — with the services it was sent to.
export function IntakeFinalCard({ card, catalog, services, withCall, notifiedAt }: {
  card: FinalCardLike;
  catalog?: Pick<IntakeCatalog, "types" | "profiles">;
  services: string[];
  withCall: boolean;
  notifiedAt?: string;
}) {
  const status = value(card.applicant_status);
  const victims = card.victims_present?.state === "negative" ? "none" as const
    : card.victims_present?.state === "known" ? { count: value(card.victims_count) } : undefined;
  return <Arm112CardView label="Итоговая карточка обучаемого" catalog={catalog} model={{
    lineState: withCall ? "разговор завершён" : "не подключен",
    aon: card.aon ? { text: card.aon } : undefined,
    providedPhone: value(card.provided_phone), onSitePhone: value(card.on_site_phone), channel: value(card.channel),
    title: `Карточка ${armCardNumber(card.number)}`,
    subtitle: [withCall ? `Вызов в ${card.call_local_time} МСК` : "Без разговора",
      notifiedAt ? `Оповещено ${new Date(notifiedAt).toLocaleString("ru-RU", { dateStyle: "short", timeStyle: "short" })}` : "Службы не оповещены"],
    applicantName: value(card.applicant_name),
    applicantStatus: status && !status.unknown ? { text: applicantStatusLabel(status.text) ?? status.text } : status,
    age: value(card.age),
    foreignLanguage: flag(card.foreign_language),
    victims,
    noOnSite: flag(card.no_on_site), noAccess: flag(card.no_access),
    address: Object.fromEntries(Object.entries(card.address ?? {}).map(([key, field]) => [key, value(field)])),
    complaint: value(card.complaint),
    typeNames: (card.incident_types ?? []).map((id) => catalog?.types.find((type) => type.id === id)?.name ?? id),
    profiles: Object.entries(card.profiles ?? {}).map(([id, profile]) => ({ id, answers: profile.answers as Record<string, IntakeProfileAnswer> })),
    unansweredText: "не заполнено",
    services,
  }} />;
}
