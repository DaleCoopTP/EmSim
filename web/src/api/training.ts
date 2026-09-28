import { useQuery } from "@tanstack/react-query";
import { api } from "./client";
import type { components } from "./schema";

export type Lesson = components["schemas"]["Lesson"];
export type LessonCreate = components["schemas"]["LessonCreate"];
export type LessonOptions = components["schemas"]["LessonOptions"];
export type Assignment = components["schemas"]["Assignment"];
export type Level = components["schemas"]["Level"];
export type LessonTiming = components["schemas"]["LessonTiming"];
export type LessonScoring = components["schemas"]["LessonScoring"];
export type LessonSettingsPatch = components["schemas"]["LessonSettingsPatch"];
export type LessonRubric = components["schemas"]["LessonRubric"];
export type ScenarioCategory = components["schemas"]["ScenarioCategory"];
export type AssignmentDrawRequest = components["schemas"]["AssignmentDrawRequest"];
export type Monitor = components["schemas"]["Monitor"];
export type MonitorRow = Monitor["rows"][number];

export const lessonsQueryKey = ["training", "lessons"] as const;
export const lessonQueryKey = (id: string) => ["training", "lesson", id] as const;
export const lessonOptionsQueryKey = ["training", "lesson-options"] as const;
export const monitorQueryKey = (id: string) => ["training", "monitor", id] as const;

export function useLessons() {
  return useQuery({ queryKey: lessonsQueryKey, queryFn: () => api.get<Lesson[]>("/lessons") });
}

export function useLesson(id: string) {
  return useQuery({
    queryKey: lessonQueryKey(id),
    queryFn: () => api.get<Lesson>(`/lessons/${encodeURIComponent(id)}`),
    enabled: id !== "",
  });
}

export function useLessonOptions() {
  return useQuery({ queryKey: lessonOptionsQueryKey, queryFn: () => api.get<LessonOptions>("/lessons/options") });
}

export function createLesson(body: LessonCreate): Promise<Lesson> {
  return api.post<Lesson>("/lessons", body);
}

export function replaceAssignments(lessonId: string, assignments: Assignment[]): Promise<Lesson> {
  return api.put<Lesson>(`/lessons/${encodeURIComponent(lessonId)}/assignments`, assignments);
}

export function startLesson(lessonId: string): Promise<Lesson> {
  return api.post<Lesson>(`/lessons/${encodeURIComponent(lessonId)}/start`);
}

export function stopLesson(lessonId: string, reason?: string): Promise<Lesson> {
  return api.post<Lesson>(`/lessons/${encodeURIComponent(lessonId)}/stop`, reason ? { reason } : {});
}

export function useMonitor(lessonId: string, enabled = true) {
  return useQuery({
    queryKey: monitorQueryKey(lessonId),
    queryFn: () => api.get<Monitor>(`/lessons/${encodeURIComponent(lessonId)}/monitor`),
    enabled: enabled && lessonId !== "",
    refetchOnWindowFocus: true,
    refetchOnReconnect: true,
  });
}

export const lessonRubricQueryKey = (id: string) => ["training", "lesson-rubric", id] as const;
export const scenarioCategoriesQueryKey = (service: string) => ["training", "scenario-categories", service] as const;

// ДДС-6/ADR-035: timing and scoring of a draft DDS lesson. scoring=null
// resets the weights and threshold to the rubric's own values.
export function updateLessonSettings(lessonId: string, patch: LessonSettingsPatch): Promise<Lesson> {
  return api.patch<Lesson>(`/lessons/${encodeURIComponent(lessonId)}`, patch);
}

export function useLessonRubric(lessonId: string, enabled = true) {
  return useQuery({
    queryKey: lessonRubricQueryKey(lessonId),
    queryFn: () => api.get<LessonRubric>(`/lessons/${encodeURIComponent(lessonId)}/rubric`),
    enabled: enabled && lessonId !== "",
  });
}

export function useScenarioCategories(enabled = true) {
  return useQuery({
    queryKey: scenarioCategoriesQueryKey(""),
    queryFn: () => api.get<ScenarioCategory[]>("/scenarios/categories"),
    enabled,
  });
}

// Proposes a random queue per row; nothing is saved until the ordinary
// replaceAssignments.
export function drawAssignments(lessonId: string, body: AssignmentDrawRequest): Promise<Assignment[]> {
  return api.post<Assignment[]>(`/lessons/${encodeURIComponent(lessonId)}/assignments/draw`, body);
}
