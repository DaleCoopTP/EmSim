import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import { Layout } from "./components/Layout";
import { RequireAuth } from "./components/RequireAuth";
import { UsersRoute } from "./routes/admin/Users";
import { WorkstationsRoute } from "./routes/admin/Workstations";
import { HomeRoute } from "./routes/Home";
import { ScenarioCatalogueRoute } from "./routes/instructor/ScenarioCatalogue";
import { ScenarioDetailRoute } from "./routes/instructor/ScenarioDetail";
import { LessonsRoute } from "./routes/instructor/Lessons";
import { LessonDetailRoute } from "./routes/instructor/LessonDetail";
import { MonitorRoute } from "./routes/instructor/Monitor";
import { LoginRoute } from "./routes/Login";
import { WorkplaceRoute } from "./routes/trainee/Workplace";

// A fresh QueryClient per app instance, not per render (App itself only
// renders once in practice, but this keeps the client from being
// recreated — and every cached query lost — on an unrelated re-render).
const queryClient = new QueryClient();

// BrowserRouter works because cmd/emsim/static.go falls back to
// index.html for any request path that does not match a real file in
// the build — the same "try_files $uri /index.html" shape a plain
// static-file server needs for client-side routing.
export default function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <Routes>
          <Route path="/login" element={<LoginRoute />} />
          <Route element={<RequireAuth />}>
            <Route element={<Layout />}>
              <Route path="/" element={<HomeRoute />} />
            </Route>
          </Route>
          <Route element={<RequireAuth roles={["admin"]} />}>
            <Route element={<Layout />}>
              <Route path="/admin/users" element={<UsersRoute />} />
              <Route path="/admin/workstations" element={<WorkstationsRoute />} />
            </Route>
          </Route>
          <Route element={<RequireAuth roles={["instructor"]} />}>
            <Route element={<Layout />}>
              <Route path="/instructor/scenarios" element={<ScenarioCatalogueRoute />} />
              <Route path="/instructor/scenarios/:scenarioId" element={<ScenarioDetailRoute />} />
              <Route path="/instructor/lessons" element={<LessonsRoute />} />
              <Route path="/instructor/lessons/:lessonId" element={<LessonDetailRoute />} />
              <Route path="/instructor/lessons/:lessonId/monitor" element={<MonitorRoute />} />
            </Route>
          </Route>
          <Route element={<RequireAuth roles={["trainee"]} />}>
            <Route element={<Layout />}>
              <Route path="/my" element={<WorkplaceRoute />} />
            </Route>
          </Route>
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
  );
}
