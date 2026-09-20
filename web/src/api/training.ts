import { useQuery } from "@tanstack/react-query";
import { api } from "./client";
import type { components } from "./schema";

export type Lesson = components["schemas"]["Lesson"];
export type LessonCreate = components["schemas"]["LessonCreate"];
export type LessonOptions = components["schemas"]["LessonOptions"];
export type Assignment = components["schemas"]["Assignment"];
export type Level = components["schemas"]["Level"];
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
