// content module's read side the SPA needs so far: GET /services (slice
// 2's C5, so the admin Users form offers a real service_code instead of
// free text) and the instructor scenario catalogue (GET /scenarios*,
// C6) — list/filter, detail, version history, and the allowlist preview
// a trainee will eventually see (slice 3 reuses the same preview shape).
// The editor mutations below (112-7/ADR-027) are this file's own later
// addition: create/copy, save-as-new-version, validate-without-saving,
// the phrase tester, approve, and the instructor's own intake catalog —
// see slice-112-7-plan.md's c7.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
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

// -------------------------------------------------------- editor (112-7/ADR-027)
//
// The editor works with exactly one shape of ScenarioBody — full_case +
// caller_mode=free_text — never the DDS or incoming_call/card_only/
// prepared-dialogue ones (slice-112-7-plan.md's own scope decision 4).
// Intake112EditorBody names that one shape so every editor hook/component
// below can be typed against it directly, instead of the wire's fully
// open ScenarioBody (exercise_type plus [key: string]: unknown).
export type Intake112Dialogue = NonNullable<components["schemas"]["Intake112Scenario"]["dialogue"]>;
export type Intake112Fact = Intake112Dialogue["facts"][number];
export type Intake112Caller = NonNullable<Intake112Dialogue["caller"]>;
export type Intake112ExpectedCard = components["schemas"]["Intake112ExpectedCard"];
export type ValidationIssue = components["schemas"]["ValidationIssue"];
export type Intake112Catalog = components["schemas"]["Intake112Catalog"];
export type ProbeMatch = { fact_id: string; kind: "reveal" | "ask" };

export interface Intake112EditorBody {
  exercise_type: "operator112_intake";
  difficulty: number;
  intake112: {
    mode: "full_case";
    caller_mode: "free_text";
    call: { aon: string; local_time: string; time_zone: "Europe/Moscow" };
    dialogue: Intake112Dialogue;
    reference: components["schemas"]["Intake112Reference"];
  };
}

export function emptyIntake112Body(difficulty = 1): Intake112EditorBody {
  return {
    exercise_type: "operator112_intake",
    difficulty,
    intake112: {
      mode: "full_case",
      caller_mode: "free_text",
      call: { aon: "+7", local_time: "12:00", time_zone: "Europe/Moscow" },
      // initial/questions are required by the wire schema (shared with
      // the prepared-dialogue shape) but must stay entirely empty for
      // free_text (internal/content/validate.go's
      // validateIntake112FreeTextDialogue) — the editor never sets them.
      dialogue: { facts: [], initial: { id: "", text: "", reveals: [] }, questions: [] },
      reference: { expected_types: [], expected_services: [], case_description: "" },
    },
  };
}

export type EditorScenario = Scenario & { version_status: NonNullable<Scenario["version_status"]>; issues: ValidationIssue[] };

export const intake112CatalogQueryKey = ["content", "intake112-catalog"] as const;

export function useIntake112Catalog() {
  return useQuery({
    queryKey: intake112CatalogQueryKey,
    queryFn: () => api.get<Intake112Catalog>("/intake112/catalog"),
  });
}

export interface CreateScenarioInput {
  title: string;
  difficulty: number;
  body?: Intake112EditorBody;
  copyFromVersionId?: string;
}

export function useCreateScenario() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateScenarioInput) =>
      api.post<EditorScenario>("/scenarios", {
        title: input.title,
        difficulty: input.difficulty,
        body: input.body,
        copy_from_version_id: input.copyFromVersionId,
      }),
    onSuccess: () => { void client.invalidateQueries({ queryKey: ["content", "scenarios"] }); },
  });
}

export interface SaveScenarioInput {
  scenarioId: string;
  baseDigest: string;
  title?: string;
  difficulty?: number;
  body: Intake112EditorBody;
}

export function useSaveScenario() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (input: SaveScenarioInput) =>
      api.put<components["schemas"]["ScenarioEditResult"]>(`/scenarios/${encodeURIComponent(input.scenarioId)}`, {
        base_digest: input.baseDigest,
        title: input.title,
        difficulty: input.difficulty,
        body: input.body,
      }),
    onSuccess: (_, input) => {
      void client.invalidateQueries({ queryKey: scenarioQueryKey(input.scenarioId) });
      void client.invalidateQueries({ queryKey: scenarioVersionsQueryKey(input.scenarioId) });
    },
  });
}

export function useValidateScenario(scenarioId: string) {
  return useMutation({
    mutationFn: (body: Intake112EditorBody) =>
      api.post<{ issues: ValidationIssue[] }>(`/scenarios/${encodeURIComponent(scenarioId)}/validate`, body),
  });
}

export function useProbeScenario(scenarioId: string) {
  return useMutation({
    mutationFn: (input: { text: string; body: Intake112EditorBody }) =>
      api.post<{ opened: ProbeMatch[] }>(`/scenarios/${encodeURIComponent(scenarioId)}/probe`, input),
  });
}

export function useApproveScenario() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (input: { scenarioId: string; versionId: string; baseDigest: string }) =>
      api.post<EditorScenario>(`/scenarios/${encodeURIComponent(input.scenarioId)}/approve`, {
        version_id: input.versionId,
        base_digest: input.baseDigest,
      }),
    onSuccess: (_, input) => {
      void client.invalidateQueries({ queryKey: scenarioQueryKey(input.scenarioId) });
      void client.invalidateQueries({ queryKey: scenarioVersionsQueryKey(input.scenarioId) });
      void client.invalidateQueries({ queryKey: ["content", "scenarios"] });
    },
  });
}

// useStartPreviewRun is POST /scenarios/{id}/preview-runs — a one-shot
// mutation the preview route calls once on mount (ScenarioPreview.tsx),
// not something a form submits repeatedly.
export function useStartPreviewRun() {
  return useMutation({
    mutationFn: (input: { scenarioId: string; versionId: string }) =>
      api.post<{ lesson_id: string; item_id: string }>(`/scenarios/${encodeURIComponent(input.scenarioId)}/preview-runs`, { version_id: input.versionId }),
  });
}
