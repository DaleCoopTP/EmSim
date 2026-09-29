import { Navigate, Outlet, useLocation } from "react-router-dom";
import type { Role } from "../api/auth";
import { useMe, type Me } from "../api/useMe";

export function RequireAuth({ roles }: { roles?: Role[] }) {
  const { data: me, isPending, isError } = useMe();
  const location = useLocation();

  if (isPending) {
    return <p>Загрузка…</p>;
  }
  if (isError) {
    return <p>Сервер недоступен. Обновите страницу.</p>;
  }
  if (!me) {
    return <Navigate to="/login" replace state={{ from: location.pathname }} />;
  }
  if (me.user.credentials_change_required && location.pathname !== "/change-password") {
    return <Navigate to="/change-password" replace />;
  }
  if (roles && !roles.includes(me.user.role)) {
    return <Navigate to="/" replace />;
  }
  return <Outlet context={me satisfies Me} />;
}
