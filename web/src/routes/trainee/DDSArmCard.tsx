import { useState, type ReactNode } from "react";
import type { CardView, Item } from "../../api/workplace";
import { formatPhone } from "../../arm112Number";
import { BellIcon, BoltIcon, CloseIcon, PrintIcon, WarningIcon, HangupIcon, LinkIcon, MessageIcon, PhoneIcon, SmsIcon, StopwatchIcon } from "../../components/Arm112Icons";
import { formatDateTime } from "../../format";
import { applicantStatusLabel, cardStatusAlarm, cardStatusLabel, reactionLabel } from "../../labels";

type DDSItem = Omit<Item, "card"> & { card: CardView };

const unavailable = "Недоступно в учебном АРМ";

// Fixed reference entries from the памятка's own АРМ-112 screenshot
// (стр. 15–16): display-only, not part of the card's notification_list.
const fixedReferenceServices = ["Префектура ЮАО", "Департамент природопользования и охраны окружающей среды"];

// DDSArmCard draws a DDS card the way the ARM-112 instruction shows a saved
// incident card (user decision 2026-09-29): the phone strip with the timer on
// the right, applicant and flags, address and description on the left, the
// incident on the right, the call log and the dark services bar. The DDS
// dispatcher never edits the card (ADR-030): only the status pencil
// (statusSlot) and the phone act.
export function DDSArmCard({ item, serverNowMs, statusSlot, comms, footer, onOpen, opening, onClose }: {
  // «Связь с бригадой» sits in the right column under the incident panels,
  // so those stretch across the card as in the instruction.
  comms?: ReactNode;
  item: DDSItem;
  serverNowMs: number;
  statusSlot: ReactNode;
  footer?: ReactNode;
  onOpen: () => void;
  opening: boolean;
  onClose: () => void;
}) {
  const card = item.card;
  const offered = item.state === "offered";
  const applicantStatus = applicantStatusLabel(card.applicant?.status);
  const victims = card.incident.victims ?? 0;
  const features = Object.entries(card.incident.features ?? {}).map(([key, value]) => `${key}: ${value}`).join("; ");
  const outgoing = item.calls.filter((call) => call.direction !== "incoming");
  const contactOf = (key?: string) => card.contacts?.find((contact) => contact.key === key);
  // ЧС/ЧП toggle as in ARM-112 but are purely visual in the trainer: they
  // are not sent to the server and are not assessed (user decision 2026-09-29).
  const [emergency, setEmergency] = useState(false);
  const [incidentFlag, setIncidentFlag] = useState(false);

  return <section className="arm112 dds-arm" aria-label={`Карточка происшествия № ${card.number}`}>
    <header className="arm112-top">
      <div className="arm112-line">
        <span className="arm112-hangup" aria-hidden="true"><HangupIcon size={26} /></span>
        <div className="arm112-line-state"><span>не подключен</span>
          <div><button type="button" disabled title={unavailable}>записи звонков</button><button type="button" disabled title={unavailable}>список SMS</button></div>
        </div>
      </div>
      <Phone label="АОН" value={card.phones?.aon} />
      <Phone label="предоставленный" value={card.phones?.provided ?? card.applicant?.phone} />
      <Phone label="телефон на место" value={card.phones?.on_site} />
      <div className="arm112-incident">
        <strong>Происшествие {card.number}</strong>
        <span>Зарег. <span className="dds-card-registration">{formatDateTime(card.registered_at)}</span>{card.channel ? ` · ${card.channel}` : ""}</span>
        <span>Статус: <span className={`card-status-badge${cardStatusAlarm(item.card_status) ? " card-status-alarm" : ""}`}>{cardStatusLabel(item.card_status)}</span></span>
      </div>
      <DDSTimer item={item} serverNowMs={serverNowMs} />
    </header>

    {offered ? <div className="arm112-incoming" role="dialog" aria-label="Новая карточка">
      <strong>Карточка № {card.number}</strong>
      <p>{card.incident.type_name ?? "Новое происшествие"} · поступила {formatDateTime(item.offered_at)}</p>
      <button type="button" disabled={opening} onClick={onOpen}>Открыть карточку</button>
    </div> : <div className="arm112-body dds-arm-body">
      <div className="arm112-strip arm112-applicant">
        <span className="dds-arm-applicant">{card.applicant?.name ?? "Заявитель не указан"}</span>
        {applicantStatus && <span className="dds-arm-muted">{applicantStatus.toLocaleLowerCase("ru-RU")}</span>}
      </div>
      <div className="dds-arm-flagrow">
      <div className="arm112-strip dds-arm-flags">
        <span>Пострадавшие: <b>{victims > 0 ? victims : "нет"}</b></span>
        <span>Отказ от скорой: <b>нет</b></span>
        <span>Заблокированные: <b>нет</b></span>
        {card.incident.danger && <span className="dds-arm-danger">Опасность: <b>{card.incident.danger}</b></span>}
      </div>
      <div className="arm112-strip dds-arm-marks">
        <button type="button" className="dds-arm-mark" aria-pressed={emergency} title="Чрезвычайная ситуация (в тренажёре не учитывается)" onClick={() => setEmergency(!emergency)}>ЧС <BoltIcon size={17} /></button>
        <button type="button" className="dds-arm-mark is-incident" aria-pressed={incidentFlag} title="Чрезвычайное происшествие (в тренажёре не учитывается)" onClick={() => setIncidentFlag(!incidentFlag)}>ЧП <WarningIcon size={17} /></button>
        <button type="button" className="dds-arm-print" aria-label="Печать" title={unavailable} disabled><PrintIcon size={20} /></button>
      </div>
      </div>

      <div className="arm112-left">
        <section className="arm112-panel dds-arm-address" aria-label="Адрес">
          <p className="dds-arm-address-line">{formatAddress(card.address)}</p>
          {card.address.okrug && <p className="dds-arm-muted">Округ: {card.address.okrug}</p>}
        </section>
        <section className="arm112-panel dds-arm-blank" aria-hidden="true" />
      </div>

      <div className="arm112-right">
        <section className="arm112-profile dds-arm-incident">
          <header><h3>{card.incident.type_name ?? "Тип не указан"}{card.incident.type_code ? ` · ${card.incident.type_code}` : ""}</h3></header>
          <p aria-label="Описание со слов заявителя">{card.incident.description ?? "Описание не указано."}</p>
        </section>
        {features && <section className="arm112-panel dds-arm-class"><span className="dds-arm-muted">Класс.:</span> <b>{features}</b></section>}
        {comms}
      </div>

      {outgoing.length > 0 && <table className="dds-arm-calls" aria-label="Журнал звонков">
        <thead><tr><th>Дата и время</th><th>Куда звонили</th><th>Телефон</th><th>Кто принял</th><th>Суть сообщения</th></tr></thead>
        <tbody>{outgoing.map((call) => <tr key={call.id}>
          <td>{formatDateTime(call.started_at)}</td>
          <td>{contactOf(call.contact_key)?.label ?? call.contact_key}</td>
          <td>{contactOf(call.contact_key)?.number ?? ""}</td>
          <td>{call.accepted_by ?? ""}</td>
          <td>{call.summary ?? (call.ended_at ? "" : "идёт разговор…")}</td>
        </tr>)}</tbody>
      </table>}
    </div>}

    {footer}

    <footer className="arm112-bar dds-arm-bar">
      <span className="arm112-bar-label">Службы:</span>
      <ul className="arm112-services" aria-label="Список оповещения">
        {card.notification_list.map((service, index) => <li key={index} className={`dds-arm-tile${service.mine ? " is-mine" : ""}`} title={service.service}>
          <strong>{service.service ?? "Служба"}</strong>
          {service.mine ? statusSlot : <span>{reactionLabel(service.status)}</span>}
        </li>)}
        {fixedReferenceServices.map((name) => <li key={name} className="dds-arm-tile" title={name}><strong>{name}</strong><span>{reactionLabel("added")}</span></li>)}
      </ul>
      <div className="arm112-bar-actions">
        <button type="button" className="arm112-bar-text dds-arm-back" onClick={onClose}>к списку происшествий</button>
        <button type="button" className="arm112-bar-square" aria-label="Связать карточки" title={unavailable} disabled><LinkIcon size={24} /></button>
        <button type="button" className="arm112-bar-square" aria-label="Напоминание" title={unavailable} disabled><StopwatchIcon size={24} /></button>
        <button type="button" className="arm112-bar-square" aria-label="Важное происшествие" title={unavailable} disabled><BellIcon size={24} /></button>
        <button type="button" className="arm112-bar-square" aria-label="Сообщение об ошибке" title={unavailable} disabled><MessageIcon size={24} /></button>
        <button type="button" className="arm112-bar-square" aria-label="К списку происшествий" title="Закрыть карточку и вернуться к списку" onClick={onClose}><CloseIcon size={24} /></button>
      </div>
    </footer>
  </section>;
}

// DDSTimer is the dark timer block in the card's top-right corner, like the
// 112 card's: it counts down the stage the card is in — opening, the
// primary decision (from the opening, ADR-035 amendment), then the work
// after it — and turns red once that stage's norm is exceeded.
function DDSTimer({ item, serverNowMs }: { item: DDSItem; serverNowMs: number }) {
  const finished = item.state === "closed" || item.state === "interrupted";
  const stage = finished ? null
    : item.state === "offered" ? { label: "открыть", deadline: item.deadlines.open_at }
      : item.deadlines.complete_at ? { label: "отработка", deadline: item.deadlines.complete_at }
        : { label: "решение", deadline: item.deadlines.primary_at };
  const seconds = stage ? Math.ceil((new Date(stage.deadline).getTime() - serverNowMs) / 1_000) : 0;
  const overdue = !!stage && seconds < 0;
  const abs = Math.abs(seconds);
  const text = stage ? `${overdue ? "+" : ""}${Math.floor(abs / 60)}:${String(abs % 60).padStart(2, "0")}` : "—:—";
  return <div className={`arm112-timer dds-arm-timer${overdue ? " is-alert" : ""}`} role="timer" aria-label={stage ? `Норматив «${stage.label}»: ${overdue ? "просрочен на" : "осталось"} ${text}` : "Карточка закрыта"}>
    <strong>{text}</strong>
    <span>{stage ? stage.label : "закрыта"}</span>
    {overdue && <small>просрочено</small>}
  </div>;
}

function Phone({ label, value }: { label: string; value?: string }) {
  return <>
    <div className="arm112-phone-side" aria-hidden="true"><PhoneIcon size={22} /><SmsIcon size={17} /></div>
    <div className="arm112-phone">
      <div className="arm112-phone-head"><span>{label}</span></div>
      <output aria-label={label} className={value ? "" : "is-empty"}>{value ? formatPhone(value) : "+7 (   )   -   -"}</output>
    </div>
  </>;
}

function formatAddress(address: CardView["address"]): string {
  if (address.text) return address.text;
  const parts = [address.city, address.district, address.street, address.house, address.building, address.entrance].filter(Boolean);
  return parts.length > 0 ? parts.join(", ") : "—";
}
