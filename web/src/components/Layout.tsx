import { useEffect, useState } from "react";
import { NavLink, Outlet, useNavigate, useOutletContext } from "react-router-dom";
import { useLogout } from "../api/auth";
import { useMaintenanceOn } from "../api/system";
import type { Me } from "../api/useMe";

const roleLabels: Record<Me["user"]["role"], string> = {
  admin: "администратор",
  instructor: "преподаватель",
  trainee: "обучаемый",
};

// Shared chrome for every signed-in screen: who is signed in, role
// navigation (admin and instructor so far; trainee navigation arrives
// with C7's workplace), and "Выйти". The Me comes
// from RequireAuth's Outlet context, so no screen fetches it twice.
export function Layout() {
  const me = useOutletContext<Me>();
  const logout = useLogout();
  const navigate = useNavigate();
  const maintenance = useMaintenanceOn();

  const onLogout = () => {
    logout.mutate(undefined, { onSettled: () => navigate("/login", { replace: true }) });
  };

  return (
    <div className="layout">
      <header className="layout-header">
        <div className="layout-brand" aria-label="112, учебный АРМ-112">
          <strong>112</strong>
          <small>учебный АРМ-112</small>
        </div>
        {me.user.role === "admin" && (
          <nav>
            <NavLink to="/admin/users">Пользователи</NavLink>
            <NavLink to="/admin/workstations">Рабочие места</NavLink>
            <NavLink to="/admin/status">Состояние</NavLink>
            <NavLink to="/admin/audit">Журнал</NavLink>
            <NavLink to="/admin/config">Конфигурация</NavLink>
          </nav>
        )}
        {me.user.role === "instructor" && (
          <nav>
            <NavLink to="/instructor/lessons">Занятия</NavLink>
            <NavLink to="/instructor/scenarios">Готовые сценарии</NavLink>
          </nav>
        )}
        {me.user.role === "trainee" && (
          <nav><NavLink to="/my">Рабочее место</NavLink><NavLink to="/my/history">История</NavLink></nav>
        )}
        <span className="layout-user">
          {me.user.full_name} · {roleLabels[me.user.role]}
          {me.workstation ? ` · ${me.workstation.label}` : ""}
        </span>
        <Clock />
        <button type="button" onClick={onLogout} disabled={logout.isPending}>
          Выйти
        </button>
      </header>
      {maintenance.on && (
        <div className="maintenance-banner" role="status">
          <strong>Технические работы.</strong> Новые занятия не запускаются, идущие продолжаются.
          {maintenance.reason ? ` ${maintenance.reason}` : ""}
        </div>
      )}
      <main className="layout-main">
        <Outlet context={me} />
      </main>
    </div>
  );
}

function Clock() {
  const [now, setNow] = useState(() => new Date());

  useEffect(() => {
    const interval = window.setInterval(() => setNow(new Date()), 1_000);
    return () => window.clearInterval(interval);
  }, []);

  return (
    <div className="layout-clock" aria-label="Текущее время">
      <time dateTime={now.toISOString()}>{now.toLocaleTimeString("ru-RU", { hour: "2-digit", minute: "2-digit" })}</time>
      <span>{now.toLocaleDateString("ru-RU", { weekday: "short", day: "2-digit", month: "short" })}</span>
    </div>
  );
}
