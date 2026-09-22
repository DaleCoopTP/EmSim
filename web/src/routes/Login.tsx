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
      <div className="login-city" aria-hidden="true">
        <div className="login-city-helicopter">⌁</div>
        <div className="login-city-skyline" />
      </div>
      <section className="login-panel" aria-labelledby="login-title">
        <div className="login-title">
          <span>112</span>
          <h1 id="login-title">Вход в систему</h1>
        </div>
        <form onSubmit={onSubmit} className="login-form">
          <label>
            <span>Логин</span>
            <input
              name="login"
              autoComplete="username"
              required
              value={form.login}
              onChange={(e) => setForm({ ...form, login: e.target.value })}
            />
          </label>
          <label>
            <span>Пароль</span>
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
            <span>Номер рабочего места (для обучаемого)</span>
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
        <p className="login-help">Учебная среда для диспетчеров ДДС. Номер РМ указывается при входе обучаемого.</p>
      </section>
    </main>
  );
}
