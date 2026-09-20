import { Navigate, useOutletContext } from "react-router-dom";
import type { Me } from "../api/useMe";

// Role home: admin and instructor go to their primary workspaces;
// the trainee waiting screen remains until C7 adds the card workplace.
export function HomeRoute() {
  const me = useOutletContext<Me>();

  switch (me.user.role) {
    case "admin":
      return <Navigate to="/admin/users" replace />;
    case "instructor":
      return <Navigate to="/instructor/lessons" replace />;
    case "trainee":
      return <Navigate to="/my" replace />;
  }
}
