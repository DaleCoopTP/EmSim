import type { ReactNode } from "react";
import type { CardPreview } from "../api/content";
import type { CardView } from "../api/workplace";
import { formatDateTime, formatOffset } from "../format";
import { applicantStatusLabel, reactionLabel } from "../labels";

// IncidentCard renders CardPreview — the same allowlist projection a
// trainee's АРМ-112 screen will show (slice 3), never the closed эталон
// (that is ScenarioReference, rendered separately by the caller). It is
// read-only everywhere it is used so far (the instructor catalogue's
// scenario detail, C6); slice 3 reuses it as-is for the live card and
// only adds action controls around it, not inside it.
// mineSlot, when given, replaces the static status of the trainee's own
// service in the service list with the live status block and its pencil
// (ADR-030); every other service stays read-only.
export function IncidentCard({ card, mineSlot }: { card: CardPreview | CardView; mineSlot?: ReactNode }) {
  const applicantStatus = applicantStatusLabel(card.applicant?.status);
  return (
    <article className="incident-card dds-card">
      <header className="incident-card-header">
        <div>
          <span className="dds-card-overline">Происшествие</span>
          <h2>№ {card.number}</h2>
        </div>
        <span className="dds-card-registration">Зарегистрировано: {"registered_at" in card ? formatDateTime(card.registered_at) : formatOffset(card.registered_at_offset_s)}</span>
      </header>
      <section className="dds-phone-strip" aria-label="Телефоны заявителя">
        <DataField label="АОН" value={card.phones?.aon} />
        <DataField label="Предоставленный" value={card.phones?.provided ?? card.applicant?.phone} />
        <DataField label="Телефон на месте" value={card.phones?.on_site} />
        <DataField label="Канал" value={card.channel} />
      </section>
      <div className="dds-card-grid">
        <section className="dds-card-section dds-card-applicant">
          <h3>Заявитель</h3>
          <p className="dds-card-value">
            {card.applicant?.name ?? "Не указан"}
            {applicantStatus ? ` · ${applicantStatus}` : ""}
          </p>
          <h3>Адрес происшествия</h3>
          <p className="dds-card-address">{formatAddress(card.address)}</p>
          {card.address.okrug && <p className="dds-card-subvalue">Округ: {card.address.okrug}</p>}
          <h3>Описание со слов заявителя</h3>
          <p className="dds-card-description">{card.incident.description ?? "Описание не указано."}</p>
        </section>
        <section className="dds-card-section dds-card-incident">
          <h3>Тип происшествия</h3>
          <p className="dds-card-type">
            {card.incident.type_name ?? "Не указан"}
            {card.incident.type_code ? ` · ${card.incident.type_code}` : ""}
          </p>
          <dl className="dds-card-details">
            <dt>Пострадавшие</dt><dd>{card.incident.victims ?? 0}</dd>
            {card.incident.danger && <><dt>Опасность</dt><dd>{card.incident.danger}</dd></>}
            {card.incident.features && Object.keys(card.incident.features).length > 0 && (
              <><dt>Признаки</dt><dd>{Object.entries(card.incident.features).map(([key, value]) => `${key}: ${value}`).join("; ")}</dd></>
            )}
          </dl>
          <h3>Контакты служб</h3>
          {card.contacts && card.contacts.length > 0 ? (
            <ul className="dds-contacts">
              {card.contacts.map((c) => <li key={c.key}><strong>{c.label}</strong><span>{c.number}</span></li>)}
            </ul>
          ) : <p className="dds-card-subvalue">Контактов нет.</p>}
        </section>
      </div>
      <section className="dds-services" aria-label="Список оповещения">
        <h3>Службы</h3>
        <div className="dds-service-list">
          {card.notification_list.map((n, i) => (
            <div key={i} className={`dds-service${n.mine ? " dds-service-mine" : ""}`}>
              <strong>{n.service ?? "Служба"}</strong>
              {n.mine && mineSlot ? mineSlot : <span>{reactionLabel(n.status)}</span>}
            </div>
          ))}
          {card.notification_list.length === 0 && <p className="dds-card-subvalue">Службы не назначены.</p>}
        </div>
      </section>
    </article>
  );
}

function DataField({ label, value }: { label: string; value: string | undefined }) {
  return <div className="dds-data-field"><span>{label}</span><strong>{value ?? "—"}</strong></div>;
}

function formatAddress(address: CardPreview["address"] | CardView["address"]): string {
  if (address.text) return address.text;
  const parts = [address.city, address.district, address.street, address.house, address.building, address.entrance].filter(
    Boolean,
  );
  return parts.length > 0 ? parts.join(", ") : "—";
}
