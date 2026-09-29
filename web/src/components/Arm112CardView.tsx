import type { ReactNode } from "react";
import type { IntakeCatalog, IntakeProfileAnswer } from "../routes/trainee/Operator112Workplace";
import { profileFieldVisible } from "../intakeProfile";
import { serviceNames, serviceTiles } from "../intakeServices";
import { GlobeIcon, HangupIcon, HelpIcon, MapIcon, PhoneIcon, PinIcon, SmsIcon, TranslateIcon } from "./Arm112Icons";
import "./IntakeReferenceCard.css";

// One shown value: fromFacts draws it in grey italics (the reference card's
// caller facts), unknown marks the operator's own "заявитель не знает".
export type CardValue = { text: string; fromFacts?: boolean; unknown?: boolean };

export type Arm112CardModel = {
  lineState: string;
  aon?: CardValue; providedPhone?: CardValue; onSitePhone?: CardValue; channel?: CardValue;
  title: string; subtitle: string[];
  applicantName?: CardValue; applicantStatus?: CardValue; age?: CardValue; foreignLanguage?: boolean;
  // undefined — not filled; "none" — «Нет»; otherwise «Есть» with an optional count.
  victims?: "none" | { count?: CardValue };
  noOnSite?: boolean; noAccess?: boolean;
  address: Record<string, CardValue | undefined>;
  complaint?: CardValue;
  descriptionNote?: ReactNode;
  typeNames: string[];
  profiles: { id: string; answers: Record<string, IntakeProfileAnswer> }[];
  // The reference leaves some profile fields open: they are not scored and
  // are drawn as one muted line. A trainee's own unanswered field is not.
  unansweredText: string;
  services: string[];
  legend?: ReactNode;
};

// Arm112CardView draws a 112 card read-only in the ARM-112 layout of the
// trainee's Operator112ProfileCase, inside a dark "monitor" frame: the
// instructor's reference card and the trainee's final card share it.
export function Arm112CardView({ model, catalog, label }: { model: Arm112CardModel; catalog?: Pick<IntakeCatalog, "profiles">; label: string }) {
  const addr = (key: string) => model.address[key];
  const addressLine = ["country", "region", "city", "street", "house", "building"].map((key) => addr(key)?.text).filter(Boolean).join(", ");
  const victims = model.victims;
  return <div className="arm112-reference-frame"><section className="arm112 arm112-reference" aria-label={label}>
    <header className="arm112-top">
      <div className="arm112-line">
        <span className="arm112-hangup" aria-hidden="true"><HangupIcon size={26} /></span>
        <div className="arm112-line-state"><span>{model.lineState}</span></div>
      </div>
      <Phone label="АОН" value={model.aon} icons={<><HelpIcon size={15} /><PinIcon size={15} /><GlobeIcon size={15} /></>} />
      <Phone label="предоставленный" value={model.providedPhone} icons={<GlobeIcon size={15} />} />
      <Phone label="телефон на место" value={model.onSitePhone} icons={<GlobeIcon size={15} />} />
      <div className="arm112-records">
        <div><button type="button" disabled>записи звонков</button><button type="button" disabled>список SMS</button></div>
        <Line value={model.channel} placeholder="канал связи" />
      </div>
      <div className="arm112-incident">
        <strong>{model.title}</strong>
        {model.subtitle.map((line, index) => <span key={index}>{line}</span>)}
      </div>
    </header>

    <div className="arm112-body">
      <div className="arm112-strip arm112-applicant">
        <Line className="arm112-reference-grow" value={model.applicantName} placeholder="Фамилия и имя заявителя" large />
        <Line value={model.applicantStatus} placeholder="выберите статус" large />
        {model.age && <span className={`arm112-reference-age${valueClass(model.age)}`}>возраст: {model.age.text}</span>}
        <span className={`arm112-square arm112-reference-square${model.foreignLanguage ? " is-pressed" : ""}`} title="Вызов на иностранном языке"><TranslateIcon size={20} /></span>
      </div>
      <div className="arm112-strip arm112-flags">
        <div className="arm112-victims"><span>Пострадавшие:</span>
          <Toggle pressed={victims === "none"}>Нет</Toggle>
          <Toggle pressed={!!victims && victims !== "none"}>Есть</Toggle>
          {victims && victims !== "none" && victims.count && <span className={valueClass(victims.count).trim() || undefined}>Количество: <strong>{victims.count.text}</strong></span>}
        </div>
        <Toggle pressed={model.noOnSite}>Нет на месте/<br />Отказ от скорой</Toggle>
        <Toggle pressed={model.noAccess}>Нет доступа/<br />Заблокированные</Toggle>
      </div>

      <div className="arm112-left">
        <section className="arm112-panel arm112-address" aria-label="Адрес">
          <div className="arm112-address-head"><span>Адрес:</span><MapIcon size={17} /></div>
          <div className="arm112-address-line"><output>{addressLine}</output></div>
          <div className="arm112-address-row arm112-cols-3">
            <Field label="Страна" value={addr("country")} /><Field label="Субъект" value={addr("region")} /><Field label="Населённый пункт" value={addr("city")} />
          </div>
          <div className="arm112-address-row arm112-cols-wide">
            <Field label="Объект" value={addr("object")} /><Field label="Округ" value={addr("okrug")} /><Field label="Район" value={addr("district")} />
          </div>
          <div className="arm112-address-row arm112-cols-wide">
            <Field label="Улица" value={addr("street")} /><Field label="Дом/Вл" value={addr("house")} /><Field label="Корпус" value={addr("building")} />
          </div>
          <div className="arm112-address-row arm112-cols-5">
            <Field label="Стр/соор" value={addr("structure")} /><Field label="Квартира/офис" value={addr("flat")} /><Field label="Подъезд" value={addr("entrance")} />
            <Field label="Этаж" value={addr("floor")} /><Field label="Код" value={addr("code")} />
          </div>
          <div className="arm112-address-row arm112-cols-wide">
            <Field label="Описательный адрес" value={addr("descriptive")} /><Field label="Ориентир" value={addr("landmark")} /><span />
          </div>
        </section>
        <section className="arm112-panel arm112-description arm112-reference-description">
          <span className="arm112-reference-caption">Описание со слов заявителя</span>
          <p className={valueClass(model.complaint).trim() || undefined}>{model.complaint?.text ?? <span className="arm112-reference-empty">не заполнено</span>}</p>
          {model.descriptionNote}
        </section>
      </div>

      <div className="arm112-right">
        <section className="arm112-panel arm112-type">
          <span className="arm112-reference-caption">Тип происшествия</span>
          {model.typeNames.length ? <div className="arm112-chosen-types">{model.typeNames.map((name) => <span key={name} className="arm112-chosen-type arm112-reference-chip">{name}</span>)}</div>
            : <p className="arm112-reference-empty">не выбран</p>}
        </section>
        {model.profiles.map((profile) => <Profile key={profile.id} profileID={profile.id} answers={profile.answers} catalog={catalog} unansweredText={model.unansweredText} />)}
      </div>
    </div>

    <footer className="arm112-bar arm112-reference-bar">
      <span className="arm112-bar-label">Службы:</span>
      <ul className="arm112-services" aria-label="Службы на вызов">
        {model.services.map((code) => <li key={code} className="arm112-service-tile is-main" title={serviceNames[code] ?? code}><PhoneIcon size={14} /><strong>{serviceTiles[code] ?? code}</strong></li>)}
        {model.services.length === 0 && <li className="arm112-reference-noservices">не выбраны</li>}
      </ul>
      {model.legend && <span className="arm112-reference-legend">{model.legend}</span>}
    </footer>
  </section></div>;
}

function Profile({ profileID, answers, catalog, unansweredText }: { profileID: string; answers: Record<string, IntakeProfileAnswer>; catalog?: Pick<IntakeCatalog, "profiles">; unansweredText: string }) {
  const definition = catalog?.profiles.find((profile) => profile.id === profileID);
  const title = (definition?.name ?? `Карта ${profileID}`).split(" · ")[0];
  return <section className="arm112-profile">
    <header><h3 title={definition?.name}>{title}</h3></header>
    {definition ? definition.fields.filter((field) => field.kind !== "shared" && profileFieldVisible(field, answers)).map((field) => {
      const answer = answers[field.id] ?? { state: "unanswered" };
      if (answer.state === "unanswered") return <div className="arm112-profile-row is-muted" key={field.id}><span>{field.label}</span>
        <span className="arm112-reference-text">{unansweredText}</span></div>;
      return <div className="arm112-profile-row" key={field.id}><span>{field.label}</span>
        <div className="arm112-options">
          {field.kind === "text"
            ? <span className="arm112-reference-text">{answer.state === "known" ? answer.value : ""}</span>
            : (field.options ?? []).map((option) => <Toggle key={option} pressed={answer.state === "known" && (answer.value === option || (answer.values ?? []).includes(option))}>{option}</Toggle>)}
          <Toggle pressed={answer.state === "unknown"}>Неизвестно</Toggle>
        </div>
      </div>;
    }) : Object.entries(answers).map(([id, answer]) => <div className="arm112-profile-row" key={id}><span>{id}</span>
      <div className="arm112-options"><Toggle pressed>{answer.state === "unknown" ? "Неизвестно" : answer.value ?? answer.values?.join(", ")}</Toggle></div></div>)}
  </section>;
}

function valueClass(value?: CardValue): string {
  return `${value?.fromFacts ? " is-from-facts" : ""}${value?.unknown ? " is-unknown-value" : ""}`;
}

function Toggle({ pressed, children }: { pressed?: boolean; children: ReactNode }) {
  return <span className={`arm112-reference-toggle${pressed ? " is-pressed" : ""}`}>{children}</span>;
}

function Phone({ label, value, icons }: { label: string; value?: CardValue; icons: ReactNode }) {
  return <>
    <div className="arm112-phone-side" aria-hidden="true"><PhoneIcon size={22} /><SmsIcon size={17} /></div>
    <div className="arm112-phone">
      <div className="arm112-phone-head"><span>{label}</span><span className="arm112-phone-icons" aria-hidden="true">{icons}</span></div>
      <output aria-label={label} className={value ? valueClass(value).trim() : "is-empty"}>{value?.text ?? "+7 (   )   -   -"}</output>
    </div>
  </>;
}

function Line({ value, placeholder, className, large }: { value?: CardValue; placeholder: string; className?: string; large?: boolean }) {
  return <span className={`arm112-reference-line${large ? " is-large" : ""}${valueClass(value)}${value ? "" : " is-empty"}${className ? ` ${className}` : ""}`}>{value?.text ?? placeholder}</span>;
}

function Field({ label, value }: { label: string; value?: CardValue }) {
  return <div className="arm112-field"><span className="arm112-reference-label">{label}:</span>
    <span className={`arm112-reference-value${valueClass(value)}${value ? "" : " is-empty"}`}>{value?.text ?? " "}</span></div>;
}
