import type { CardPreview } from "../api/content";
import { formatOffset } from "../format";
import { applicantStatusLabel, reactionLabel } from "../labels";

// IncidentCard renders CardPreview — the same allowlist projection a
// trainee's АРМ-112 screen will show (slice 3), never the closed эталон
// (that is ScenarioReference, rendered separately by the caller). It is
// read-only everywhere it is used so far (the instructor catalogue's
// scenario detail, C6); slice 3 reuses it as-is for the live card and
// only adds action controls around it, not inside it.
export function IncidentCard({ card }: { card: CardPreview }) {
  const applicantStatus = applicantStatusLabel(card.applicant?.status);
  return (
    <section className="incident-card">
      <header className="incident-card-header">
        <h2>Карточка № {card.number}</h2>
        <span>{formatOffset(card.registered_at_offset_s)}</span>
      </header>
      <dl>
        <dt>Заявитель</dt>
        <dd>
          {card.applicant?.name ?? "—"}
          {applicantStatus ? ` (${applicantStatus})` : ""}
          {card.applicant?.phone ? `, ${card.applicant.phone}` : ""}
        </dd>
        <dt>Адрес</dt>
        <dd>{formatAddress(card.address)}</dd>
        <dt>Происшествие</dt>
        <dd>
          {card.incident.type_name ?? "—"}
          {card.incident.type_code ? ` (${card.incident.type_code})` : ""}
        </dd>
        {card.incident.description && (
          <>
            <dt>Описание</dt>
            <dd>{card.incident.description}</dd>
          </>
        )}
        {card.incident.features && Object.keys(card.incident.features).length > 0 && (
          <>
            <dt>Признаки</dt>
            <dd>
              {Object.entries(card.incident.features)
                .map(([key, value]) => `${key}: ${value}`)
                .join("; ")}
            </dd>
          </>
        )}
        <dt>Пострадавшие</dt>
        <dd>{card.incident.victims ?? 0}</dd>
        {card.incident.danger && (
          <>
            <dt>Опасность</dt>
            <dd>{card.incident.danger}</dd>
          </>
        )}
        <dt>Телефоны</dt>
        <dd>{formatPhones(card.phones)}</dd>
        {card.channel && (
          <>
            <dt>Канал</dt>
            <dd>{card.channel}</dd>
          </>
        )}
      </dl>

      <h3>Список оповещения</h3>
      <table>
        <thead>
          <tr>
            <th>Служба</th>
            <th>Статус</th>
            <th>Моя</th>
          </tr>
        </thead>
        <tbody>
          {card.notification_list.map((n, i) => (
            <tr key={i}>
              <td>{n.service}</td>
              <td>{reactionLabel(n.status)}</td>
              <td>{n.mine ? "да" : ""}</td>
            </tr>
          ))}
        </tbody>
      </table>

      <h3>Контакты</h3>
      {card.contacts && card.contacts.length > 0 ? (
        <ul>
          {card.contacts.map((c) => (
            <li key={c.key}>
              {c.label}: {c.number}
            </li>
          ))}
        </ul>
      ) : (
        <p>Контактов нет.</p>
      )}
    </section>
  );
}

function formatAddress(address: CardPreview["address"]): string {
  if (address.text) return address.text;
  const parts = [address.city, address.district, address.street, address.house, address.building, address.entrance].filter(
    Boolean,
  );
  return parts.length > 0 ? parts.join(", ") : "—";
}

function formatPhones(phones: CardPreview["phones"]): string {
  if (!phones) return "—";
  const parts: string[] = [];
  if (phones.aon) parts.push(`АОН ${phones.aon}`);
  if (phones.provided) parts.push(`указан ${phones.provided}`);
  if (phones.on_site) parts.push(`на месте ${phones.on_site}`);
  return parts.length > 0 ? parts.join(", ") : "—";
}
