import { useState, type FormEvent } from "react";
import { Navigate, useLocation, useNavigate } from "react-router-dom";
import { useLogin } from "../api/auth";
import { errorMessage } from "../api/errors";
import { useMe } from "../api/useMe";

// The workstation field is always shown: whether it is required depends
// on the role behind the login, which the client does not know before
// the server answers (422 details.field=workstation_no when a trainee
// leaves it blank). Login/password rules live on the server too — the
// form only refuses to submit obviously empty credentials.
export function LoginRoute() {
  const { data: me, isPending: meIsPending } = useMe();
  const login = useLogin();
  const navigate = useNavigate();
  const location = useLocation();
  const [form, setForm] = useState({ login: "", password: "", workstationNo: "" });

  if (!meIsPending && me) {
    return <Navigate to="/" replace />;
  }

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    const workstationNo = form.workstationNo.trim();
    login.mutate(
      {
        login: form.login.trim(),
        password: form.password,
        ...(workstationNo !== "" ? { workstation_no: Number(workstationNo) } : {}),
      },
      {
        onSuccess: () => {
          const from = (location.state as { from?: string } | null)?.from;
          navigate(from && from !== "/login" ? from : "/", { replace: true });
        },
      },
    );
  };

  return (
    <main className="login">
      <form onSubmit={onSubmit} className="login-form">
        <h1>EmSim — вход</h1>
        <label>
          Логин
          <input
            name="login"
            autoComplete="username"
            required
            value={form.login}
            onChange={(e) => setForm({ ...form, login: e.target.value })}
          />
        </label>
        <label>
          Пароль
          <input
            name="password"
            type="password"
            autoComplete="current-password"
            required
            value={form.password}
            onChange={(e) => setForm({ ...form, password: e.target.value })}
          />
        </label>
        <label>
          Номер рабочего места (для обучаемого)
          <input
            name="workstation_no"
            type="number"
            min={1}
            inputMode="numeric"
            value={form.workstationNo}
            onChange={(e) => setForm({ ...form, workstationNo: e.target.value })}
          />
        </label>
        {login.isError && <p role="alert" className="error">{errorMessage(login.error)}</p>}
        <button type="submit" disabled={login.isPending}>
          {login.isPending ? "Вход…" : "Войти"}
        </button>
      </form>
    </main>
  );
}
