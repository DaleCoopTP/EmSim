import { useState } from "react";
import type { Intake112Catalog, Intake112DescriptionQuestion, Intake112ExpectedCard } from "../api/content";
import type { components } from "../api/schema";
import { applicantStatuses, serviceTiles } from "../intakeServices";
import { profileFieldVisible } from "../intakeProfile";
import { ProfileAnswerField } from "../routes/trainee/Operator112ProfileCase";
import type { IntakeProfileAnswer } from "../routes/trainee/Operator112Workplace";
import { CloseIcon, HangupIcon, MapIcon, PhoneIcon, SmsIcon } from "./Arm112Icons";
import "./IntakeReferenceCard.css";

type Reference = components["schemas"]["Intake112Reference"];
type Address = NonNullable<Intake112ExpectedCard["address"]>;
type AddressKey = keyof Address;
type ExpectedProfiles = NonNullable<Reference["expected_profiles"]>;

// newDescriptionQuestion follows the editor's own random-id convention
// (ADR-028): a fresh row's id only needs to be unique within this form.
function newDescriptionQuestion(): Intake112DescriptionQuestion {
  return { id: `q_${Math.random().toString(36).slice(2, 8)}`, question: "" };
}

// Reference profile values are a string, an option list or {"state":"unknown"}
// (Intake112Reference.expected_profiles); the trainee's answer editor works
// on IntakeProfileAnswer, so values are converted both ways.
function toAnswer(raw: unknown): IntakeProfileAnswer {
  if (typeof raw === "string") return { state: "known", value: raw };
  if (Array.isArray(raw)) return { state: "known", values: raw.map(String) };
  if (raw && typeof raw === "object" && (raw as { state?: string }).state === "unknown") return { state: "unknown" };
  return { state: "unanswered" };
}
function fromAnswer(answer: IntakeProfileAnswer): unknown {
  if (answer.state === "unknown") return { state: "unknown" };
  if (answer.state === "known") return answer.values ?? answer.value;
  return undefined;
}

// IntakeReferenceEditor lets the instructor fill intake112.reference as the
// ARM-112 card the operator is expected to end up with: the same layout the
// trainee works in and the reference card the scenario page shows. Only
// fields rubric-v2/v3 score are editable; an empty field is not part of the
// reference.
export function IntakeReferenceEditor({ reference, onChange, catalog, services, aon, localTime }: {
  reference: Reference;
  onChange: (patch: Partial<Reference>) => void;
  catalog?: Intake112Catalog;
  services: { code: string; name: string }[];
  aon: string;
  localTime: string;
}) {
  const card = reference.expected_card ?? {};
  const address = card.address ?? {};
  const types = reference.expected_types ?? [];
  const chosenServices = reference.expected_services ?? [];
  const questions = reference.description_questions ?? [];
  const profiles = reference.expected_profiles ?? {};
  // "Есть" stays pressed while the count is being retyped (briefly empty).
  const [victimsTyping, setVictimsTyping] = useState(false);
  const victimsPresent = victimsTyping || (card.victims_count ?? 0) > 0;

  const updateCard = (patch: Partial<Intake112ExpectedCard>) => onChange({ expected_card: { ...card, ...patch } });
  const updateAddress = (key: AddressKey, value: string) => updateCard({ address: { ...address, [key]: value || undefined } });
  const updateQuestions = (next: Intake112DescriptionQuestion[]) => onChange({ description_questions: next });

  const profileIDsFor = (typeIDs: string[]) => new Set(typeIDs.flatMap((id) => catalog?.types.find((type) => type.id === id)?.profile_ids ?? []));
  const toggleType = (id: string) => {
    const next = types.includes(id) ? types.filter((t) => t !== id) : [...types, id];
    const owned = profileIDsFor(next);
    // A profile whose type was removed leaves the reference with it:
    // validate.go rejects expected_profiles outside the chosen types.
    const kept = Object.fromEntries(Object.entries(profiles).filter(([profileID]) => owned.has(profileID)));
    onChange({ expected_types: next, expected_profiles: Object.keys(kept).length ? kept : undefined });
  };
  const toggleService = (code: string) =>
    onChange({ expected_services: chosenServices.includes(code) ? chosenServices.filter((c) => c !== code) : [...chosenServices, code] });

  const updateProfileAnswer = (profileID: string, fieldID: string, answer: IntakeProfileAnswer) => {
    const definition = catalog?.profiles.find((profile) => profile.id === profileID);
    const values: Record<string, unknown> = { ...(profiles[profileID] ?? {}) };
    const raw = fromAnswer(answer);
    if (raw === undefined) delete values[fieldID];
    else values[fieldID] = raw;
    // A field hidden by visible_when is not part of the reference
    // (validate.go's hidden_field): drop answers whose condition no longer
    // holds, repeating while one removal hides another field.
    for (let changed = true; changed;) {
      changed = false;
      const answers = Object.fromEntries(Object.entries(values).map(([id, v]) => [id, toAnswer(v)]));
      for (const field of definition?.fields ?? []) {
        if (field.id in values && !profileFieldVisible(field, answers)) { delete values[field.id]; changed = true; }
      }
    }
    const next: ExpectedProfiles = { ...profiles };
    if (Object.keys(values).length) next[profileID] = values;
    else delete next[profileID];
    onChange({ expected_profiles: Object.keys(next).length ? next : undefined });
  };

  const statusKnown = !card.applicant_status || applicantStatuses.some(([value]) => value === card.applicant_status);
  const addressLine = (["country", "region", "city", "street", "house", "building"] as const).map((key) => address[key]).filter(Boolean).join(", ");
  const field = (key: AddressKey, label: string) => (
    <div className="arm112-field">
      <label><span>{label}:</span>
        <input aria-label={`Эталон: ${label}`} value={address[key] ?? ""} onChange={(e) => updateAddress(key, e.target.value)} />
      </label>
    </div>
  );
  const shownProfiles = [...profileIDsFor(types)].map((id) => catalog?.profiles.find((profile) => profile.id === id)).filter((p) => !!p);

  return <div className="arm112-reference-frame"><section className="arm112 arm112-reference arm112-reference-edit" aria-label="Эталонная карточка">
    <header className="arm112-top">
      <div className="arm112-line">
        <span className="arm112-hangup" aria-hidden="true"><HangupIcon size={26} /></span>
        <div className="arm112-line-state"><span>эталон</span></div>
      </div>
      <div className="arm112-phone-side" aria-hidden="true"><PhoneIcon size={22} /><SmsIcon size={17} /></div>
      <div className="arm112-phone">
        <div className="arm112-phone-head"><span>АОН</span></div>
        <output aria-label="АОН" className={aon.length > 2 ? "" : "is-empty"}>{aon.length > 2 ? aon : "+7 (   )   -   -"}</output>
      </div>
      <div className="arm112-incident">
        <strong>Эталон карточки</strong>
        <span>{`Вызов в ${localTime} МСК · АОН и время — на вкладке «Общее»`}</span>
        <span>Незаполненное поле в эталон не входит</span>
      </div>
    </header>

    <div className="arm112-body">
      <div className="arm112-strip arm112-applicant">
        <input className="arm112-plain" aria-label="Эталон: ФИО заявителя" placeholder="Фамилия и имя заявителя" maxLength={1000}
          value={card.applicant_name ?? ""} onChange={(e) => updateCard({ applicant_name: e.target.value || undefined })} />
        <select className="arm112-plain" aria-label="Эталон: статус заявителя" value={card.applicant_status ?? ""}
          onChange={(e) => updateCard({ applicant_status: e.target.value || undefined })}>
          <option value="">статус не задан</option>
          {applicantStatuses.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
          {!statusKnown && <option value={card.applicant_status}>{card.applicant_status}</option>}
        </select>
        <label className="arm112-reference-age-input">возраст:
          <input aria-label="Эталон: возраст" inputMode="numeric" maxLength={3} value={card.age ?? ""}
            onChange={(e) => { const digits = e.target.value.replace(/\D/g, ""); updateCard({ age: digits ? Number(digits) : undefined }); }} />
        </label>
      </div>
      <div className="arm112-strip arm112-flags">
        <div className="arm112-victims"><span>Пострадавшие:</span>
          <button type="button" aria-pressed={card.victims_count === 0}
            onClick={() => { setVictimsTyping(false); updateCard({ victims_count: card.victims_count === 0 ? undefined : 0 }); }}>Нет</button>
          <button type="button" aria-pressed={victimsPresent}
            onClick={() => { setVictimsTyping(!victimsPresent); updateCard({ victims_count: victimsPresent ? undefined : 1 }); }}>Есть</button>
          {victimsPresent && <label>Количество: <input aria-label="Эталон: число пострадавших" inputMode="numeric" maxLength={4}
            value={card.victims_count ?? ""} onChange={(e) => { const digits = e.target.value.replace(/\D/g, ""); updateCard({ victims_count: digits ? Number(digits) : undefined }); }} /></label>}
        </div>
      </div>

      <div className="arm112-left">
        <section className="arm112-panel arm112-address" aria-label="Адрес">
          <div className="arm112-address-head"><span>Адрес:</span><MapIcon size={17} /></div>
          <div className="arm112-address-line"><output aria-label="Адрес целиком">{addressLine}</output></div>
          <div className="arm112-address-row arm112-cols-3">{field("country", "Страна")}{field("region", "Субъект")}{field("city", "Населённый пункт")}</div>
          <div className="arm112-address-row arm112-cols-3">{field("okrug", "Округ")}{field("district", "Район")}<span /></div>
          <div className="arm112-address-row arm112-cols-wide">{field("street", "Улица")}{field("house", "Дом/Вл")}{field("building", "Корпус")}</div>
          <div className="arm112-address-row arm112-cols-5">{field("structure", "Стр/соор")}{field("flat", "Квартира/офис")}{field("entrance", "Подъезд")}{field("floor", "Этаж")}<span /></div>
          <p className="arm112-reference-caption">Объект, ориентир, код и описательный адрес в автооценку не входят.</p>
        </section>
        <section className="arm112-panel arm112-description">
          <label><span>Описание со слов заявителя</span>
            <textarea aria-label="Эталон: описание со слов заявителя" placeholder="как его должен записать оператор" maxLength={1999}
              value={card.complaint ?? ""} onChange={(e) => updateCard({ complaint: e.target.value || undefined })} /></label>
          <div className="arm112-reference-questions-edit">
            <span className="arm112-reference-caption">Что должно быть отражено в описании — вопросы ИИ-судье (ADR-028)</span>
            <p className="arm112-reference-hint">Судья отвечает да/нет/нужна проверка только по тексту этого поля — без разговора и других полей. Формулируйте положительно: «Указано ли, что …?». Без вопросов блок оценивается в 0, модель не вызывается.</p>
            <ol>
              {questions.map((question, index) => <li key={question.id}>
                <input aria-label={`Вопрос ${index + 1}`} value={question.question} placeholder="Указано ли, что …?"
                  onChange={(e) => updateQuestions(questions.map((q, i) => (i === index ? { ...q, question: e.target.value } : q)))} />
                <button type="button" className="arm112-x" aria-label={`Убрать вопрос ${index + 1}`} onClick={() => updateQuestions(questions.filter((_, i) => i !== index))}><CloseIcon size={16} /></button>
              </li>)}
            </ol>
            <button type="button" className="arm112-small" onClick={() => updateQuestions([...questions, newDescriptionQuestion()])}>+ добавить вопрос</button>
          </div>
        </section>
      </div>

      <div className="arm112-right">
        <section className="arm112-panel arm112-type" aria-label="Тип происшествия">
          <span className="arm112-reference-caption">Тип происшествия</span>
          <div className="arm112-quick-types">
            {catalog?.types.map((type) => <button type="button" key={type.id} aria-pressed={types.includes(type.id)} onClick={() => toggleType(type.id)}>{type.name}</button>)}
          </div>
        </section>
        {shownProfiles.map((profile) => <section className="arm112-profile" key={profile.id} aria-label={profile.name}>
          <header><h3 title={profile.name}>{profile.name.split(" · ")[0]}</h3></header>
          {profile.fields.filter((f) => f.kind !== "shared").map((f) => ({ f, answers: Object.fromEntries(Object.entries(profiles[profile.id] ?? {}).map(([id, v]) => [id, toAnswer(v)])) }))
            .filter(({ f, answers }) => profileFieldVisible(f, answers))
            .map(({ f, answers }) => <div className="arm112-profile-row" key={f.id}><span>{f.label}</span>
              <ProfileAnswerField label={f.label} kind={f.kind as "single" | "multiple" | "text"} options={f.options ?? []}
                answer={answers[f.id] ?? { state: "unanswered" }} disabled={false} onChange={(answer) => updateProfileAnswer(profile.id, f.id, answer)} />
            </div>)}
        </section>)}
      </div>
    </div>

    <footer className="arm112-bar arm112-reference-bar">
      <span className="arm112-bar-label">Службы<br />(отметьте нужные):</span>
      <ul className="arm112-services" aria-label="Ожидаемые службы">
        {services.map((service) => <li key={service.code}>
          <button type="button" className="arm112-service-toggle" aria-pressed={chosenServices.includes(service.code)} aria-label={service.name} title={service.name}
            onClick={() => toggleService(service.code)}>{serviceTiles[service.code] ?? service.name}</button>
        </li>)}
      </ul>
    </footer>
  </section></div>;
}
