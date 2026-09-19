import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import { HomeRoute } from "./routes/Home";
import { LoginRoute } from "./routes/Login";

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
          <Route path="/" element={<HomeRoute />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
  );
}
