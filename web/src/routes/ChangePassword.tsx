import { useState, type FormEvent } from "react";
import { Navigate, useNavigate } from "react-router-dom";
import { useChangePassword, useLogout } from "../api/auth";
import { errorMessage } from "../api/errors";
import { useMe } from "../api/useMe";

// ADR-038: shown instead of the workspace while the account's password is
// one an administrator set (credentials_change_required). Nothing else is
// reachable until it is replaced — the server answers 403
// password_change_required to every other request — but "Выйти" always is.
export function ChangePasswordRoute() {
  const { data: me } = useMe();
  const change = useChangePassword();
  const logout = useLogout();
  const navigate = useNavigate();
  const [form, setForm] = useState({ current: "", next: "", repeat: "" });
  const mismatch = form.repeat !== "" && form.next !== form.repeat;

  if (me && !me.user.credentials_change_required) {
    return <Navigate to="/" replace />;
  }

  const onSubmit = (event: FormEvent) => {
    event.preventDefault();
    if (mismatch) return;
    change.mutate(
      { current_password: form.current, new_password: form.next },
      { onSuccess: () => navigate("/", { replace: true }) },
    );
  };

  return (
    <main className="login">
      <div className="login-city" aria-hidden="true" />
      <section className="login-panel" aria-labelledby="change-title">
        <div className="login-title">
          <span>112</span>
          <h1 id="change-title">Смена пароля</h1>
        </div>
        <p className="login-help">Пароль задал администратор. Придумайте свой, чтобы продолжить работу.</p>
        <form onSubmit={onSubmit} className="login-form">
          <label>
            <span>Текущий пароль</span>
            <input
              name="current_password"
              type="password"
              autoComplete="current-password"
              required
              value={form.current}
              onChange={(e) => setForm({ ...form, current: e.target.value })}
            />
          </label>
          <label>
            <span>Новый пароль</span>
            <input
              name="new_password"
              type="password"
              autoComplete="new-password"
              required
              value={form.next}
              onChange={(e) => setForm({ ...form, next: e.target.value })}
            />
          </label>
          <label>
            <span>Повторите новый пароль</span>
            <input
              name="repeat_password"
              type="password"
              autoComplete="new-password"
              required
              value={form.repeat}
              onChange={(e) => setForm({ ...form, repeat: e.target.value })}
            />
          </label>
          {mismatch && <p role="alert" className="error">Пароли не совпадают.</p>}
          {change.isError && <p role="alert" className="error">{errorMessage(change.error)}</p>}
          <button type="submit" disabled={change.isPending || mismatch}>
            Сменить пароль
          </button>
          <button type="button" onClick={() => logout.mutate()}>
            Выйти
          </button>
        </form>
      </section>
    </main>
  );
}
