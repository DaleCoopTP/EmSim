import { Navigate, useOutletContext } from "react-router-dom";
import type { Me } from "../api/useMe";

// Role home (slice-planning.md §2 happy path step 4): admin goes straight
// to user management; instructor has nothing to do until lessons exist
// (slice 3); trainee sees exactly what the DoD asks for — full name,
// service, chosen workstation, and "Ожидайте назначения занятия".
export function HomeRoute() {
  const me = useOutletContext<Me>();

  switch (me.user.role) {
    case "admin":
      return <Navigate to="/admin/users" replace />;
    case "instructor":
      return (
        <section>
          <h1>{me.user.full_name}</h1>
          <p>Занятия появятся в срезе 3.</p>
        </section>
      );
    case "trainee":
      return (
        <section>
          <h1>{me.user.full_name}</h1>
          <dl>
            <dt>Служба</dt>
            <dd>{me.user.service_code ?? "—"}</dd>
            <dt>Рабочее место</dt>
            <dd>{me.workstation ? `${me.workstation.label} (№ ${me.workstation.number})` : "не выбрано"}</dd>
          </dl>
          <p className="notice">Ожидайте назначения занятия.</p>
        </section>
      );
  }
}
