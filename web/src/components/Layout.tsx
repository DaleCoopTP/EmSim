import { NavLink, Outlet, useNavigate, useOutletContext } from "react-router-dom";
import { useLogout } from "../api/auth";
import type { Me } from "../api/useMe";

const roleLabels: Record<Me["user"]["role"], string> = {
  admin: "администратор",
  instructor: "преподаватель",
  trainee: "обучаемый",
};

// Shared chrome for every signed-in screen: who is signed in, role
// navigation (admin and instructor so far; trainee has nothing to
// navigate to until slice 3's lessons exist), and "Выйти". The Me comes
// from RequireAuth's Outlet context, so no screen fetches it twice.
export function Layout() {
  const me = useOutletContext<Me>();
  const logout = useLogout();
  const navigate = useNavigate();

  const onLogout = () => {
    logout.mutate(undefined, { onSettled: () => navigate("/login", { replace: true }) });
  };

  return (
    <div className="layout">
      <header className="layout-header">
        <strong>EmSim</strong>
        {me.user.role === "admin" && (
          <nav>
            <NavLink to="/admin/users">Пользователи</NavLink>
            <NavLink to="/admin/workstations">Рабочие места</NavLink>
          </nav>
        )}
        {me.user.role === "instructor" && (
          <nav>
            <NavLink to="/instructor/scenarios">Сценарии</NavLink>
          </nav>
        )}
        <span className="layout-user">
          {me.user.full_name} · {roleLabels[me.user.role]}
          {me.workstation ? ` · ${me.workstation.label}` : ""}
        </span>
        <button type="button" onClick={onLogout} disabled={logout.isPending}>
          Выйти
        </button>
      </header>
      <main className="layout-main">
        <Outlet context={me} />
      </main>
    </div>
  );
}
