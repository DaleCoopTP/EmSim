import { useState } from "react";
import { auditCsvUrl, useAuditLog, useUsers, type AuditFilter, type AuditRow } from "../../api/admin";
import { auditActionLabels, auditOutcomeLabels } from "../../adminLabels";
import { errorMessage } from "../../api/errors";
import { formatDateTime } from "../../format";

// ADR-038: the audit log for the administrator — who did what, when and
// with what result. Actors are shown by login; the log never carries
// passwords, trainee texts or full names. The filter form applies on
// "Показать" so a half-typed period does not fire a request per key.
const actionGroups: { value: string; label: string }[] = [
  { value: "", label: "Все действия" },
  { value: "auth.", label: "Вход и выход" },
  { value: "admin.", label: "Действия администратора" },
  { value: "lesson.", label: "Занятия" },
  { value: "scenario.", label: "Сценарии" },
  { value: "assessment.", label: "Оценки" },
  { value: "item.", label: "Команды обучаемых" },
  { value: "content.import.", label: "Импорт" },
];

const emptyDraft = { from: "", to: "", actorId: "", action: "", outcome: "" };

// <input type="date"> values are local days: the period runs from the
// start of "from" to the start of the day after "to".
function toFilter(draft: typeof emptyDraft): AuditFilter {
  const startOf = (day: string, plusDays: number) => {
    const d = new Date(`${day}T00:00:00`);
    d.setDate(d.getDate() + plusDays);
    return d.toISOString();
  };
  return {
    from: draft.from ? startOf(draft.from, 0) : undefined,
    to: draft.to ? startOf(draft.to, 1) : undefined,
    actorId: draft.actorId || undefined,
    action: draft.action || undefined,
    outcome: draft.outcome || undefined,
  };
}

export function AuditRoute() {
  const [draft, setDraft] = useState(emptyDraft);
  const [filter, setFilter] = useState<AuditFilter>({});
  const users = useUsers(1, 200);
  const log = useAuditLog(filter);
  const rows = log.data?.pages.flatMap((page) => page.items) ?? [];
  const periodInvalid = draft.from !== "" && draft.to !== "" && draft.to < draft.from;

  return (
    <section className="admin-audit">
      <div className="page-heading">
        <div>
          <h1>Журнал</h1>
          <p>Кто, что и когда сделал. Записи хранятся ограниченное время, старые удаляются автоматически.</p>
        </div>
      </div>
      <form
        className="status-panel audit-filter"
        onSubmit={(event) => {
          event.preventDefault();
          if (!periodInvalid) setFilter(toFilter(draft));
        }}
      >
        <label>
          С даты
          <input type="date" value={draft.from} onChange={(e) => setDraft({ ...draft, from: e.target.value })} />
        </label>
        <label>
          По дату
          <input type="date" value={draft.to} onChange={(e) => setDraft({ ...draft, to: e.target.value })} />
        </label>
        <label>
          Пользователь
          <select value={draft.actorId} onChange={(e) => setDraft({ ...draft, actorId: e.target.value })}>
            <option value="">Все</option>
            {users.data?.items.map((u) => (
              <option key={u.id} value={u.id}>
                {u.login}
              </option>
            ))}
          </select>
        </label>
        <label>
          Действие
          <select value={draft.action} onChange={(e) => setDraft({ ...draft, action: e.target.value })}>
            {actionGroups.map((g) => (
              <option key={g.value} value={g.value}>
                {g.label}
              </option>
            ))}
          </select>
        </label>
        <label>
          Результат
          <select value={draft.outcome} onChange={(e) => setDraft({ ...draft, outcome: e.target.value })}>
            <option value="">Любой</option>
            {Object.entries(auditOutcomeLabels).map(([value, label]) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
          </select>
        </label>
        <div className="audit-filter-actions">
          <button type="submit" disabled={periodInvalid}>
            Показать
          </button>
          <button
            type="button"
            onClick={() => {
              setDraft(emptyDraft);
              setFilter({});
            }}
          >
            Сбросить
          </button>
          <a className="button-link" href={auditCsvUrl(filter)} download="audit.csv">
            Скачать CSV
          </a>
        </div>
        {periodInvalid && <p className="error">Конец периода раньше его начала.</p>}
      </form>

      {log.isPending && <p>Загрузка…</p>}
      {log.isError && <p className="error">{errorMessage(log.error)}</p>}
      {log.data && (
        <div className="status-panel">
          {rows.length === 0 ? (
            <p>Записей нет.</p>
          ) : (
            <table>
              <thead>
                <tr>
                  <th>Время</th>
                  <th>Пользователь</th>
                  <th>Действие</th>
                  <th>Результат</th>
                  <th>Подробности</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((row) => (
                  <AuditLine key={row.id} row={row} />
                ))}
              </tbody>
            </table>
          )}
          {log.hasNextPage && (
            <p>
              <button type="button" onClick={() => log.fetchNextPage()} disabled={log.isFetchingNextPage}>
                {log.isFetchingNextPage ? "Загрузка…" : "Показать ещё"}
              </button>
            </p>
          )}
        </div>
      )}
    </section>
  );
}

function AuditLine({ row }: { row: AuditRow }) {
  const details = Object.entries(row.details)
    .map(([key, value]) => `${key}: ${typeof value === "object" ? JSON.stringify(value) : String(value)}`)
    .join(", ");
  return (
    <tr className={row.outcome === "ok" ? undefined : `audit-${row.outcome}`}>
      <td>{formatDateTime(row.at)}</td>
      <td>{row.actor_login ?? (row.actor_id ? "удалён" : "система")}{row.actor_role ? ` (${row.actor_role})` : ""}</td>
      <td title={row.action}>{auditActionLabels[row.action] ?? row.action}</td>
      <td>{auditOutcomeLabels[row.outcome] ?? row.outcome}</td>
      <td><code>{details || "—"}</code></td>
    </tr>
  );
}
