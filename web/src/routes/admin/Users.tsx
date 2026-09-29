import { useState, type ChangeEvent, type FormEvent } from "react";
import { importIssues, useCreateUser, useImportUsers, useRevokeUserSessions, useUpdateUser, useUserSessions, useUsers, type User, type UserCreate, type UserPatch } from "../../api/admin";
import { useServices } from "../../api/content";
import { errorMessage } from "../../api/errors";
import type { components } from "../../api/schema";
import { formatDateTime } from "../../format";

type Role = components["schemas"]["Role"];

const roles: Role[] = ["admin", "instructor", "trainee"];
const pageSize = 50;

export function UsersRoute() {
  const [page, setPage] = useState(1);
  const users = useUsers(page, pageSize);
  const [editing, setEditing] = useState<User | "new" | null>(null);
  const [sessionsFor, setSessionsFor] = useState<User | null>(null);
  const [importing, setImporting] = useState(false);
  const update = useUpdateUser();

  const total = users.data?.total ?? 0;
  const pages = Math.max(1, Math.ceil(total / pageSize));

  return (
    <section>
      <h1>Пользователи</h1>
      <p>
        <button type="button" onClick={() => setEditing("new")}>
          Создать пользователя
        </button>{" "}
        <button type="button" onClick={() => setImporting(true)}>
          Загрузить из CSV
        </button>
      </p>

      {importing && <ImportPanel onDone={() => setImporting(false)} />}

      {editing === "new" && <CreateUserForm onDone={() => setEditing(null)} />}
      {editing && editing !== "new" && <EditUserForm key={editing.id} user={editing} onDone={() => setEditing(null)} />}

      {sessionsFor && <SessionsPanel key={sessionsFor.id} user={sessionsFor} onDone={() => setSessionsFor(null)} />}
      {update.isError && <p role="alert" className="error">{errorMessage(update.error)}</p>}

      {users.isPending && <p>Загрузка…</p>}
      {users.isError && <p className="error">{errorMessage(users.error)}</p>}
      {users.data && (
        <>
          <table>
            <thead>
              <tr>
                <th>Логин</th>
                <th>ФИО</th>
                <th>Роль</th>
                <th>Служба</th>
                <th>Уровень</th>
                <th>Активен</th>
                <th>Вход</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {users.data.items.map((u) => (
                <tr key={u.id} className={u.active ? undefined : "inactive"}>
                  <td>{u.login}</td>
                  <td>{u.full_name}</td>
                  <td>{u.role}</td>
                  <td>{u.service_code ?? "—"}</td>
                  <td>{u.level}</td>
                  <td>{u.active ? "да" : "нет"}</td>
                  <td>
                    {u.locked_until && <span className="user-locked">Заблокирован до {formatDateTime(u.locked_until)}</span>}
                    {!u.locked_until && u.credentials_change_required && <span>Ждёт смены пароля</span>}
                  </td>
                  <td>
                    <button type="button" onClick={() => setEditing(u)}>
                      Изменить
                    </button>{" "}
                    {u.locked_until && (
                      <button type="button" disabled={update.isPending} onClick={() => update.mutate({ id: u.id, patch: { unlock: true } })}>
                        Разблокировать
                      </button>
                    )}{" "}
                    <button type="button" onClick={() => setSessionsFor(u)}>
                      Сеансы
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          <p>
            Всего: {total}. Страница {page} из {pages}.{" "}
            <button type="button" disabled={page <= 1} onClick={() => setPage(page - 1)}>
              ←
            </button>{" "}
            <button type="button" disabled={page >= pages} onClick={() => setPage(page + 1)}>
              →
            </button>
          </p>
        </>
      )}
    </section>
  );
}

// ADR-038: the user's live sessions, and "end them all". Timings and
// workstation only — the session's token is never sent to the client.
function SessionsPanel({ user, onDone }: { user: User; onDone: () => void }) {
  const sessions = useUserSessions(user.id);
  const revoke = useRevokeUserSessions();
  return (
    <div className="status-panel user-sessions">
      <div className="status-panel-heading">
        <h2>Сеансы: {user.login}</h2>
        <span>
          <button type="button" disabled={revoke.isPending || (sessions.data?.length ?? 0) === 0} onClick={() => revoke.mutate(user.id)}>
            Завершить все сеансы
          </button>{" "}
          <button type="button" onClick={onDone}>
            Закрыть
          </button>
        </span>
      </div>
      {sessions.isPending && <p>Загрузка…</p>}
      {sessions.isError && <p className="error">{errorMessage(sessions.error)}</p>}
      {revoke.isError && <p className="error">{errorMessage(revoke.error)}</p>}
      {sessions.data && sessions.data.length === 0 && <p>Действующих сеансов нет.</p>}
      {sessions.data && sessions.data.length > 0 && (
        <table>
          <thead>
            <tr><th>Начат</th><th>Последняя активность</th><th>Истекает</th><th>РМ</th></tr>
          </thead>
          <tbody>
            {sessions.data.map((s) => (
              <tr key={`${s.created_at}-${s.expires_at}`}>
                <td>{formatDateTime(s.created_at)}</td>
                <td>{formatDateTime(s.last_seen_at)}</td>
                <td>{formatDateTime(s.expires_at)}</td>
                <td>{s.workstation_number === null ? "—" : `${s.workstation_number}${s.workstation_label ? ` (${s.workstation_label})` : ""}`}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

const issueFieldLabels: Record<string, string> = {
  file: "файл",
  login: "логин",
  full_name: "ФИО",
  role: "роль",
  service_code: "служба",
};

const issueReasonLabels: Record<string, string> = {
  empty: "файл пуст",
  too_many_rows: "больше 300 строк",
  malformed_csv: "не удалось разобрать CSV",
  missing_column: "нет такой колонки в заголовке",
  invalid: "недопустимое значение",
  taken: "логин уже занят",
  unknown: "неизвестная служба",
  not_allowed: "для этой роли не задаётся",
  blank: "пусто",
  required: "обязательно",
  too_long: "слишком длинное",
};

function issueText(field: string, reason: string): string {
  const label = issueFieldLabels[field] ?? field;
  const text = reason.startsWith("duplicate_of_row_") ? `повторяет строку ${reason.slice("duplicate_of_row_".length)}` : (issueReasonLabels[reason] ?? reason);
  return `${label}: ${text}`;
}

// ADR-038: bulk creation from a table. The file is checked first ("Проверить"
// creates nobody); "Создать" makes everyone or nobody and returns the
// server-made passwords, shown here once and offered as a printable sheet —
// nothing on the server keeps them. The sheet is built in the browser from
// this one answer.
function ImportPanel({ onDone }: { onDone: () => void }) {
  const run = useImportUsers();
  const [csv, setCsv] = useState("");
  const [fileName, setFileName] = useState("");
  const result = run.data;
  const issues = run.isError ? importIssues(run.error) : [];
  const created = result && !result.dry_run ? result : null;

  const onFile = async (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    if (!file) return;
    setFileName(file.name);
    setCsv(await file.text());
    run.reset();
  };

  const downloadSheet = () => {
    if (!created) return;
    const lines = ["логин;пароль;ФИО;роль", ...created.users.map((u) => [u.login, u.password ?? "", u.full_name, u.role].join(";"))];
    const blob = new Blob(["\ufeff" + lines.join("\r\n") + "\r\n"], { type: "text/csv;charset=utf-8" });
    const link = document.createElement("a");
    link.href = URL.createObjectURL(blob);
    link.download = "new-users.csv";
    link.click();
    URL.revokeObjectURL(link.href);
  };

  return (
    <div className="status-panel user-import">
      <div className="status-panel-heading">
        <h2>Загрузка пользователей из CSV</h2>
        <button type="button" onClick={onDone}>
          Закрыть
        </button>
      </div>
      {!created && (
        <>
          <p>
            Первая строка — заголовок: <code>login</code>, <code>full_name</code>, <code>role</code> и, если нужна, <code>service_code</code>. Разделитель — запятая или точка с запятой. Пароли создаёт сервер и показывает один раз.
          </p>
          <p>
            <input type="file" accept=".csv,text/csv,text/plain" aria-label="Файл CSV" onChange={onFile} />
            {fileName && <span> {fileName}</span>}
          </p>
          <p>
            <button type="button" disabled={csv === "" || run.isPending} onClick={() => run.mutate({ csv, dryRun: true })}>
              Проверить
            </button>{" "}
            <button type="button" disabled={csv === "" || run.isPending || !(result?.dry_run)} onClick={() => run.mutate({ csv, dryRun: false })}>
              Создать пользователей
            </button>
          </p>
          {run.isError && issues.length === 0 && <p role="alert" className="error">{errorMessage(run.error)}</p>}
          {issues.length > 0 && (
            <div role="alert" className="error">
              <p>Файл не принят, никто не создан:</p>
              <ul>
                {issues.map((issue, i) => (
                  <li key={i}>{issue.row > 0 ? `строка ${issue.row} — ` : ""}{issueText(issue.field, issue.reason)}</li>
                ))}
              </ul>
            </div>
          )}
          {result?.dry_run && (
            <>
              <p className="notice">Файл в порядке: будет создано пользователей — {result.count}. Пароли появятся после «Создать пользователей».</p>
              <table>
                <thead>
                  <tr><th>Строка</th><th>Логин</th><th>ФИО</th><th>Роль</th><th>Служба</th></tr>
                </thead>
                <tbody>
                  {result.users.map((u) => (
                    <tr key={u.row}>
                      <td>{u.row}</td><td>{u.login}</td><td>{u.full_name}</td><td>{u.role}</td><td>{u.service_code ?? "—"}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </>
          )}
        </>
      )}
      {created && (
        <>
          <p className="notice">
            Создано пользователей: {created.count}. <strong>Пароли показаны один раз</strong> — сохраните лист сейчас, потом их узнать нельзя.
          </p>
          <p>
            <button type="button" onClick={downloadSheet}>
              Скачать лист «логин — пароль»
            </button>
          </p>
          <table>
            <thead>
              <tr><th>Логин</th><th>Пароль</th><th>ФИО</th><th>Роль</th></tr>
            </thead>
            <tbody>
              {created.users.map((u) => (
                <tr key={u.row}>
                  <td>{u.login}</td><td><code>{u.password}</code></td><td>{u.full_name}</td><td>{u.role}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
    </div>
  );
}

function CreateUserForm({ onDone }: { onDone: () => void }) {
  const create = useCreateUser();
  const [form, setForm] = useState({
    login: "",
    password: "",
    full_name: "",
    role: "trainee" as Role,
    service_code: "",
  });

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    const body: UserCreate = {
      login: form.login.trim(),
      password: form.password,
      full_name: form.full_name.trim(),
      role: form.role,
      ...(form.role === "trainee" && form.service_code.trim() ? { service_code: form.service_code.trim() } : {}),
    };
    create.mutate(body, { onSuccess: onDone });
  };

  return (
    <form onSubmit={onSubmit} className="user-form">
      <h2>Новый пользователь</h2>
      <label>
        Логин
        <input required value={form.login} onChange={(e) => setForm({ ...form, login: e.target.value })} />
      </label>
      <label>
        Пароль
        <input
          type="password"
          required
          autoComplete="new-password"
          value={form.password}
          onChange={(e) => setForm({ ...form, password: e.target.value })}
        />
      </label>
      <label>
        ФИО
        <input required value={form.full_name} onChange={(e) => setForm({ ...form, full_name: e.target.value })} />
      </label>
      <RoleServiceFields
        role={form.role}
        serviceCode={form.service_code}
        onChange={(patch) => setForm({ ...form, ...patch })}
      />
      {create.isError && <p role="alert" className="error">{errorMessage(create.error)}</p>}
      <p>
        <button type="submit" disabled={create.isPending}>
          Создать
        </button>{" "}
        <button type="button" onClick={onDone}>
          Отмена
        </button>
      </p>
    </form>
  );
}

// Edit sends only the fields that actually changed (PATCH semantics,
// openapi.yaml UserPatch): an empty password box means "keep it", a
// role change away from trainee sends service_code:null so the server's
// role/service_code rule is satisfied in one request.
function EditUserForm({ user, onDone }: { user: User; onDone: () => void }) {
  const update = useUpdateUser();
  const [form, setForm] = useState({
    password: "",
    full_name: user.full_name,
    role: user.role,
    service_code: user.service_code ?? "",
    active: user.active,
  });

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    const patch: UserPatch = {};
    if (form.password !== "") patch.password = form.password;
    if (form.full_name.trim() !== user.full_name) patch.full_name = form.full_name.trim();
    if (form.role !== user.role) patch.role = form.role;
    if (form.active !== user.active) patch.active = form.active;
    const wantService = form.role === "trainee" ? (form.service_code.trim() || null) : null;
    if (wantService !== (user.service_code ?? null)) patch.service_code = wantService;
    update.mutate({ id: user.id, patch }, { onSuccess: onDone });
  };

  return (
    <form onSubmit={onSubmit} className="user-form">
      <h2>Изменить: {user.login}</h2>
      <label>
        Новый пароль (пусто — не менять; смена отзывает сессии)
        <input
          type="password"
          autoComplete="new-password"
          value={form.password}
          onChange={(e) => setForm({ ...form, password: e.target.value })}
        />
      </label>
      <label>
        ФИО
        <input required value={form.full_name} onChange={(e) => setForm({ ...form, full_name: e.target.value })} />
      </label>
      <RoleServiceFields
        role={form.role}
        serviceCode={form.service_code}
        onChange={(patch) => setForm({ ...form, ...patch })}
      />
      <label>
        <input type="checkbox" checked={form.active} onChange={(e) => setForm({ ...form, active: e.target.checked })} />{" "}
        Активен (отключение отзывает сессии)
      </label>
      {update.isError && <p role="alert" className="error">{errorMessage(update.error)}</p>}
      <p>
        <button type="submit" disabled={update.isPending}>
          Сохранить
        </button>{" "}
        <button type="button" onClick={onDone}>
          Отмена
        </button>
      </p>
    </form>
  );
}

// RoleServiceFields' service_code is a <select> populated from GET
// /services (internal/content, slice 2), not free text — the server
// already rejects an unknown code with 422 (internal/auth.Service's
// ServiceCatalog check, C5), but picking from the real catalogue is
// both faster for the admin and avoids that round trip on a typo.
function RoleServiceFields({
  role,
  serviceCode,
  onChange,
}: {
  role: Role;
  serviceCode: string;
  onChange: (patch: { role?: Role; service_code?: string }) => void;
}) {
  const services = useServices();
  return (
    <>
      <label>
        Роль
        <select value={role} onChange={(e) => onChange({ role: e.target.value as Role })}>
          {roles.map((r) => (
            <option key={r} value={r}>
              {r}
            </option>
          ))}
        </select>
      </label>
      {role === "trainee" && (
        <label>
          Служба
          {services.isError && <p role="alert" className="error">{errorMessage(services.error)}</p>}
          <select
            value={serviceCode}
            disabled={services.isPending}
            onChange={(e) => onChange({ service_code: e.target.value })}
          >
            <option value="">
              {services.isPending ? "Загрузка…" : "Без службы (оператор 112)"}
            </option>
            {serviceCode !== "" && !services.data?.some((s) => s.code === serviceCode) && (
              <option value={serviceCode}>{serviceCode} (неизвестна каталогу)</option>
            )}
            {services.data?.map((s) => (
              <option key={s.code} value={s.code}>
                {s.name} ({s.code})
              </option>
            ))}
          </select>
        </label>
      )}
    </>
  );
}
