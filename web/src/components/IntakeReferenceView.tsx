import type { IntakeCatalog } from "../routes/trainee/Operator112Workplace";
import { applicantStatusLabel } from "../labels";
import { expectedProfileText } from "../intakeProfile";

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
};

const addressLabels: [string, string][] = [["country", "Страна"], ["region", "Субъект"], ["city", "Населённый пункт"], ["okrug", "Округ"],
  ["district", "Район"], ["street", "Улица"], ["house", "Дом"], ["building", "Корпус"], ["structure", "Строение"], ["flat", "Квартира"],
  ["entrance", "Подъезд"], ["floor", "Этаж"]];


// IntakeReferenceView is the instructor's readable form of a 112 case's
// closed reference: the main card fields ADDRESS_FIELDS/P_APPLICANT_NAME
// score and every profile-card answer PROFILE_CARDS scores, labelled from
// the intake catalog when it is available.
export function IntakeReferenceView({ reference, catalog }: { reference: IntakeReferenceLike; catalog?: Pick<IntakeCatalog, "types" | "profiles"> }) {
  const card = reference.expected_card;
  const typeName = (id: string) => catalog?.types.find((type) => type.id === id)?.name ?? id;
  const profiles = Object.entries(reference.expected_profiles ?? {});
  const address = addressLabels.filter(([key]) => card?.address?.[key]);
  return <>
    <h3>Ожидаемая карточка</h3>
    {card ? <dl>
      {reference.expected_types && <><dt>Тип происшествия</dt><dd>{reference.expected_types.map(typeName).join(", ")}</dd></>}
      {card.applicant_status && <><dt>Статус заявителя</dt><dd>{applicantStatusLabel(card.applicant_status)}</dd></>}
      {card.applicant_name && <><dt>ФИО заявителя</dt><dd>{card.applicant_name}</dd></>}
      {card.age !== undefined && <><dt>Возраст</dt><dd>{card.age}</dd></>}
      {card.victims_count !== undefined && <><dt>Пострадавшие</dt><dd>{card.victims_count}</dd></>}
      {address.map(([key, label]) => <div key={key}><dt>{label}</dt><dd>{card.address?.[key]}</dd></div>)}
      {card.complaint && <><dt>Описание (ориентир)</dt><dd>{card.complaint}</dd></>}
    </dl> : <p>Эталон карточки не задан — блок адреса получит 0.</p>}
    <h3>Профильные карты</h3>
    {profiles.length === 0 ? <p>Эталон профильных карт не задан — блок карт получит 0.</p> : profiles.map(([profileID, answers]) => {
      const definition = catalog?.profiles.find((profile) => profile.id === profileID);
      const fields = definition ? definition.fields.filter((field) => field.id in answers).map((field) => [field.id, field.label] as const)
        : Object.keys(answers).map((id) => [id, id] as const);
      return <section key={profileID} className="intake-review-card"><h4>{definition?.name ?? profileID}</h4><dl>
        {fields.map(([id, label]) => <div key={id}><dt>{label}</dt><dd>{expectedProfileText(answers[id])}</dd></div>)}
      </dl></section>;
    })}
  </>;
}
