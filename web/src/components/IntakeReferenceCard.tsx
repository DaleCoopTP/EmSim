import type { IntakeCatalog, IntakeProfileAnswer } from "../routes/trainee/Operator112Workplace";
import { applicantStatusLabel } from "../labels";
import { Arm112CardView, type CardValue } from "./Arm112CardView";

// Loose shape of intake112.reference as the instructor API returns it; the
// generated schema types expected_profiles values as `unknown`.
export type IntakeReferenceLike = {
  expected_types?: string[];
  expected_services?: string[];
  expected_card?: {
    applicant_status?: string; applicant_name?: string; age?: number; victims_count?: number; complaint?: string;
    address?: Record<string, string | undefined>;
  };
  expected_profiles?: Record<string, Record<string, unknown>>;
  description_questions?: { id: string; question: string }[];
};

// A caller fact with a card_path says which card field the operator should
// fill from it. Facts are not the answer key: they only complete fields the
// reference does not score (a landmark, a callback phone).
export type IntakeFactLike = { id: string; card_path?: string; knowledge: string; value?: string };

const addressKeys = ["country", "region", "city", "object", "okrug", "district", "street", "house", "building", "structure", "flat", "entrance", "floor", "code", "descriptive", "landmark"];

// IntakeReferenceCard shows a 112 case's closed reference as the card the
// operator is expected to end up with. Values scored by rubric-v2 come from
// intake112.reference; the other card fields are filled from the caller
// facts and drawn in grey italics.
export function IntakeReferenceCard({ reference, catalog, aon, localTime, facts, withCall }: {
  reference: IntakeReferenceLike;
  catalog?: Pick<IntakeCatalog, "types" | "profiles">;
  aon?: string;
  localTime?: string;
  facts?: IntakeFactLike[];
  withCall: boolean;
}) {
  const card = reference.expected_card ?? {};
  const fromFacts = new Map<string, string>();
  for (const fact of facts ?? []) {
    if (fact.card_path && fact.knowledge !== "unknown" && fact.value) fromFacts.set(fact.card_path, fact.value);
  }
  const value = (path: string, scored: string | number | undefined): CardValue | undefined => {
    if (scored !== undefined && scored !== "") return { text: String(scored) };
    const fact = fromFacts.get(path);
    return fact ? { text: fact, fromFacts: true } : undefined;
  };
  const status = value("/applicant_status", card.applicant_status);
  const victims = value("/victims_count", card.victims_count);
  const questions = reference.description_questions ?? [];
  const asAnswer = (raw: unknown): IntakeProfileAnswer => {
    if (typeof raw === "string") return { state: "known", value: raw };
    if (Array.isArray(raw)) return { state: "known", values: raw.map(String) };
    if (raw && typeof raw === "object" && (raw as { state?: string }).state === "unknown") return { state: "unknown" };
    return { state: "unanswered" };
  };

  return <Arm112CardView label="Эталонная карточка" catalog={catalog} model={{
    lineState: withCall ? "разговор завершён" : "не подключен",
    aon: aon ? { text: aon } : undefined,
    providedPhone: value("/provided_phone", undefined),
    onSitePhone: value("/on_site_phone", undefined),
    channel: value("/channel", undefined),
    title: "Эталон карточки",
    subtitle: [`Эталон · ${localTime ? `вызов в ${localTime} МСК` : "без разговора"}`, "Итог обработки для сверки"],
    applicantName: value("/applicant_name", card.applicant_name),
    applicantStatus: status ? { ...status, text: applicantStatusLabel(status.text) ?? status.text } : undefined,
    age: value("/age", card.age),
    victims: victims ? victims.text === "0" ? "none" : { count: victims } : undefined,
    address: Object.fromEntries(addressKeys.map((key) => [key, value(`/address/${key}`, card.address?.[key])])),
    complaint: value("/complaint", card.complaint),
    descriptionNote: questions.length > 0 && <div className="arm112-reference-questions">
      <span className="arm112-reference-caption">В описании должно быть отражено (проверка ИИ-судьёй):</span>
      <ul>{questions.map((question) => <li key={question.id}>{question.question.replace(/^Указан[оаы]? ли,? (что )?/, "").replace(/\?$/, "")}</li>)}</ul>
    </div>,
    typeNames: (reference.expected_types ?? []).map((id) => catalog?.types.find((type) => type.id === id)?.name ?? id),
    profiles: Object.entries(reference.expected_profiles ?? {}).map(([id, answers]) => ({ id, answers: Object.fromEntries(Object.entries(answers).map(([field, raw]) => [field, asAnswer(raw)])) })),
    unansweredText: "не оценивается",
    services: reference.expected_services ?? [],
    legend: fromFacts.size > 0 ? <><i>Серым курсивом</i> — из фактов заявителя, в автооценку не входит</> : undefined,
  }} />;
}
