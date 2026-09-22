// content module's read side the SPA needs so far: GET /services (slice
// 2's C5, so the admin Users form offers a real service_code instead of
// free text) and the instructor scenario catalogue (GET /scenarios*,
// C6) — list/filter, detail, version history, and the allowlist preview
// a trainee will eventually see (slice 3 reuses the same preview shape).
import { useQuery } from "@tanstack/react-query";
import { api } from "./client";
import type { components } from "./schema";

export type Service = components["schemas"]["Service"];

export const servicesQueryKey = ["content", "services"] as const;

export function useServices() {
  return useQuery({
    queryKey: servicesQueryKey,
    queryFn: () => api.get<Service[]>("/services"),
  });
}

export type ScenarioSummary = components["schemas"]["ScenarioSummary"];
export type Scenario = components["schemas"]["Scenario"];
export type ScenarioVersionSummary = components["schemas"]["ScenarioVersionSummary"];
export type CardPreview = components["schemas"]["CardPreview"];
export type ScenarioReference = components["schemas"]["ScenarioReference"];
export type ContactPreview = components["schemas"]["Contact"];
export type ScenarioStatus = NonNullable<ScenarioSummary["status"]>;

export interface ScenarioList {
  items: ScenarioSummary[];
  total: number;
}

// Filter mirrors GET /scenarios' query params (internal/content/http's
// listScenarios): difficultyMin/Max map to difficulty_min/max, both
// 1..10 and server-validated (422 on an inverted range) — the filter UI
// only needs to send what it has, not pre-validate.
export interface ScenarioFilter {
  service?: string;
  exerciseType?: ScenarioSummary["exercise_type"];
  status?: ScenarioStatus;
  difficultyMin?: number;
  difficultyMax?: number;
  page: number;
  pageSize: number;
}

function scenarioQuery(filter: ScenarioFilter): string {
  const params = new URLSearchParams();
  if (filter.service) params.set("service", filter.service);
  if (filter.exerciseType) params.set("exercise_type", filter.exerciseType);
  if (filter.status) params.set("status", filter.status);
  if (filter.difficultyMin) params.set("difficulty_min", String(filter.difficultyMin));
  if (filter.difficultyMax) params.set("difficulty_max", String(filter.difficultyMax));
  params.set("page", String(filter.page));
  params.set("page_size", String(filter.pageSize));
  return params.toString();
}

export const scenariosQueryKey = (filter: ScenarioFilter) => ["content", "scenarios", filter] as const;

export function useScenarios(filter: ScenarioFilter) {
  return useQuery({
    queryKey: scenariosQueryKey(filter),
    queryFn: () => api.get<ScenarioList>(`/scenarios?${scenarioQuery(filter)}`),
    placeholderData: (previous) => previous,
  });
}

export const scenarioQueryKey = (id: string) => ["content", "scenario", id] as const;

export function useScenario(id: string) {
  return useQuery({
    queryKey: scenarioQueryKey(id),
    queryFn: () => api.get<Scenario>(`/scenarios/${encodeURIComponent(id)}`),
    enabled: id !== "",
  });
}

export const scenarioVersionsQueryKey = (id: string) => ["content", "scenario", id, "versions"] as const;

export function useScenarioVersions(id: string) {
  return useQuery({
    queryKey: scenarioVersionsQueryKey(id),
    queryFn: () => api.get<ScenarioVersionSummary[]>(`/scenarios/${encodeURIComponent(id)}/versions`),
  });
}

export type ScenarioPreview =
  | { card: CardPreview; reference: ScenarioReference }
  | { exercise_type: "operator112_intake"; intake112: components["schemas"]["Intake112Scenario"] };

export const scenarioPreviewQueryKey = (id: string) => ["content", "scenario", id, "preview"] as const;

export function useScenarioPreview(id: string) {
  return useQuery({
    queryKey: scenarioPreviewQueryKey(id),
    queryFn: () => api.get<ScenarioPreview>(`/scenarios/${encodeURIComponent(id)}/preview`),
  });
}
