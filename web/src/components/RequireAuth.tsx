import { Navigate, Outlet, useLocation } from "react-router-dom";
import type { Role } from "../api/auth";
import { useMe, type Me } from "../api/useMe";

// Client-side route guard. The server is the real authorization
// boundary (every protected endpoint checks the session and role
// itself, CLAUDE.md "authorization and ownership checks... on the
// server"); this only keeps a signed-out or wrong-role user from seeing
// a screen whose every request would fail anyway. A signed-out user is
// sent to /login with the intended location remembered; a wrong-role
// user is sent to their own home.
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
  if (roles && !roles.includes(me.user.role)) {
    return <Navigate to="/" replace />;
  }
  return <Outlet context={me satisfies Me} />;
}
