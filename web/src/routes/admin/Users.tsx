import { useState, type FormEvent } from "react";
import { useCreateUser, useUpdateUser, useUsers, type User, type UserCreate, type UserPatch } from "../../api/admin";
import { useServices } from "../../api/content";
import { errorMessage } from "../../api/errors";
import type { components } from "../../api/schema";

type Role = components["schemas"]["Role"];

const roles: Role[] = ["admin", "instructor", "trainee"];
const pageSize = 50;

export function UsersRoute() {
  const [page, setPage] = useState(1);
  const users = useUsers(page, pageSize);
  const [editing, setEditing] = useState<User | "new" | null>(null);

  const total = users.data?.total ?? 0;
  const pages = Math.max(1, Math.ceil(total / pageSize));

  return (
    <section>
      <h1>Пользователи</h1>
      <p>
        <button type="button" onClick={() => setEditing("new")}>
          Создать пользователя
        </button>
      </p>

      {editing === "new" && <CreateUserForm onDone={() => setEditing(null)} />}
      {editing && editing !== "new" && <EditUserForm key={editing.id} user={editing} onDone={() => setEditing(null)} />}

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
                    <button type="button" onClick={() => setEditing(u)}>
                      Изменить
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
      ...(form.role === "trainee" ? { service_code: form.service_code.trim() } : {}),
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
    const wantService = form.role === "trainee" ? form.service_code.trim() : null;
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
            required
            value={serviceCode}
            disabled={services.isPending}
            onChange={(e) => onChange({ service_code: e.target.value })}
          >
            <option value="" disabled>
              {services.isPending ? "Загрузка…" : "Выберите службу"}
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
