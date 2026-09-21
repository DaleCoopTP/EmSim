import { useQuery } from "@tanstack/react-query";
import { api } from "./client";
import type { components } from "./schema";

export type Assessment = components["schemas"]["Assessment"];
export type AssessmentRevision = components["schemas"]["AssessmentRevision"];
export type CriterionResult = components["schemas"]["CriterionResult"];
export type LessonAssessmentRow = components["schemas"]["LessonAssessmentRow"];

export interface Evidence {
  final_card?: { number?: string; address?: { text?: string; okrug?: string }; applicant?: { name?: string } };
  actions?: Array<{ action_id: string; type: string; accepted: boolean; server_at: string; payload?: unknown; effect?: unknown }>;
  comments?: Array<{ seq: number; text: string }>;
  events?: Array<{ key: string; state: string; delivered_at?: string | null; late?: boolean }>;
  calls?: Array<{ call_id: string; contact_key: string; started_at: string; ended_at?: string | null; accepted_by?: string | null; summary?: string | null; recording_sha256?: string | null }>;
  derived?: { open_seconds?: number | null; primary_seconds?: number | null; work_seconds?: number | null; total_seconds?: number };
}

export interface AssessmentDetail {
  automatic_state: LessonAssessmentRow["automatic_state"];
  final: Assessment | null;
  revisions: Assessment[];
  rubric_effective: { version?: string; criteria?: Array<{ id: string; disabled?: boolean; weight?: number; critical?: boolean }> };
  evidence: Evidence;
}

export const lessonAssessmentsQueryKey = (lessonId: string) => ["assessment", "lesson", lessonId] as const;
export const assessmentQueryKey = (itemId: string) => ["assessment", "item", itemId] as const;

export function useLessonAssessments(lessonId: string) {
  return useQuery({ queryKey: lessonAssessmentsQueryKey(lessonId), queryFn: () => api.get<LessonAssessmentRow[]>(`/lessons/${encodeURIComponent(lessonId)}/assessments`), enabled: lessonId !== "" });
}

export function useAssessment(itemId: string) {
  return useQuery({ queryKey: assessmentQueryKey(itemId), queryFn: () => api.get<AssessmentDetail>(`/items/${encodeURIComponent(itemId)}/assessment`), enabled: itemId !== "", refetchInterval: (query) => {
    const state = query.state.data?.automatic_state;
    return state === "waiting" || state === "pending" || state === "leased" ? 2000 : false;
  } });
}

export function createAssessmentRevision(itemId: string, body: AssessmentRevision): Promise<Assessment> {
  return api.post<Assessment>(`/items/${encodeURIComponent(itemId)}/assessment/revisions`, body);
}
