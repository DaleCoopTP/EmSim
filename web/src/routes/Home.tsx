import { Navigate } from "react-router-dom";
import { useMe } from "../api/useMe";

// Route skeleton only (slice 1 commit plan, C8): proves the session
// round-trips through useMe() and the client-side redirect works. The
// actual per-role screens (admin's user/workstation management, the
// trainee's "ФИО/служба/РМ/ожидайте назначения занятия") land in C9 —
// the server is the real authorization boundary regardless (CLAUDE.md:
// "authorization and ownership checks... on the server"), so this
// redirect is a UX convenience, not a security control.
export function HomeRoute() {
  const { data: me, isPending } = useMe();

  if (isPending) {
    return <p>Загрузка…</p>;
  }
  if (!me) {
    return <Navigate to="/login" replace />;
  }

  return (
    <main>
      <h1>EmSim</h1>
      <p>
        Вы вошли как {me.user.full_name} ({me.user.role}).
      </p>
      <p>Экран для этой роли появится в следующем коммите (срез 1, C9).</p>
    </main>
  );
}
