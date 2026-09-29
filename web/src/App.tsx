import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import { Layout } from "./components/Layout";
import { RequireAuth } from "./components/RequireAuth";
import { UsersRoute } from "./routes/admin/Users";
import { WorkstationsRoute } from "./routes/admin/Workstations";
import { StatusRoute } from "./routes/admin/Status";
import { AuditRoute } from "./routes/admin/Audit";
import { ConfigRoute } from "./routes/admin/Config";
import { ReportsRoute } from "./routes/admin/Reports";
import { HomeRoute } from "./routes/Home";
import { ScenarioCatalogueRoute } from "./routes/instructor/ScenarioCatalogue";
import { ScenarioDetailRoute } from "./routes/instructor/ScenarioDetail";
import { ScenarioEditorRoute } from "./routes/instructor/ScenarioEditor";
import { ScenarioPreviewRoute } from "./routes/instructor/ScenarioPreview";
import { LessonsRoute } from "./routes/instructor/Lessons";
import { LessonDetailRoute } from "./routes/instructor/LessonDetail";
import { MonitorRoute } from "./routes/instructor/Monitor";
import { LessonAssessmentsRoute } from "./routes/instructor/LessonAssessments";
import { ItemReviewRoute } from "./routes/instructor/ItemReview";
import { LessonReportRoute } from "./routes/instructor/LessonReport";
import { LoginRoute } from "./routes/Login";
import { ChangePasswordRoute } from "./routes/ChangePassword";
import { WorkplaceRoute } from "./routes/trainee/Workplace";
import { HistoryRoute } from "./routes/trainee/History";

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
            <Route path="/change-password" element={<ChangePasswordRoute />} />
            <Route element={<Layout />}>
              <Route path="/" element={<HomeRoute />} />
            </Route>
          </Route>
          <Route element={<RequireAuth roles={["admin"]} />}>
            <Route element={<Layout />}>
              <Route path="/admin/users" element={<UsersRoute />} />
              <Route path="/admin/workstations" element={<WorkstationsRoute />} />
              <Route path="/admin/status" element={<StatusRoute />} />
              <Route path="/admin/audit" element={<AuditRoute />} />
              <Route path="/admin/config" element={<ConfigRoute />} />
              <Route path="/admin/reports" element={<ReportsRoute />} />
            </Route>
          </Route>
          <Route element={<RequireAuth roles={["instructor"]} />}>
            <Route element={<Layout />}>
              <Route path="/instructor/scenarios" element={<ScenarioCatalogueRoute />} />
              <Route path="/instructor/scenarios/new" element={<ScenarioEditorRoute />} />
              <Route path="/instructor/scenarios/:scenarioId" element={<ScenarioDetailRoute />} />
              <Route path="/instructor/scenarios/:scenarioId/edit" element={<ScenarioEditorRoute />} />
              <Route path="/instructor/preview/:itemId" element={<ScenarioPreviewRoute />} />
              <Route path="/instructor/lessons" element={<LessonsRoute />} />
              <Route path="/instructor/lessons/:lessonId" element={<LessonDetailRoute />} />
              <Route path="/instructor/lessons/:lessonId/monitor" element={<MonitorRoute />} />
              <Route path="/instructor/lessons/:lessonId/assessments" element={<LessonAssessmentsRoute />} />
              <Route path="/instructor/lessons/:lessonId/report" element={<LessonReportRoute />} />
              <Route path="/instructor/items/:itemId/review" element={<ItemReviewRoute />} />
            </Route>
          </Route>
          <Route element={<RequireAuth roles={["trainee"]} />}>
            <Route element={<Layout />}>
              <Route path="/my" element={<WorkplaceRoute />} />
              <Route path="/my/history" element={<HistoryRoute />} />
            </Route>
          </Route>
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
  );
}
