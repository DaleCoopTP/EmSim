import { useQuery } from "@tanstack/react-query";
import { api } from "./client";

export type AssessmentStatus = "ready" | "needs_review" | "unavailable" | "pending" | "not_assessed";

export interface PublicError {
  criterion_id: string;
  status: "partial" | "not_met";
  label: string;
  guide_ref: string | null;
}

export interface IntakeBlockScore {
  criterion_id: string;
  label: string;
  points: number | null;
  max_points: number;
}

export interface ReportItem {
  item_id: string;
  lesson_id: string;
  lesson_title: string;
  card_number: string;
  difficulty: number;
  closed_at: string;
  item_state: "closed" | "interrupted";
  // ДДС-3/ADR-032: set only for a dds_processing row, undefined for
  // operator112_intake (it has no equivalent notion).
  card_status?: "registered" | "not_notified" | "in_progress" | "refused" | "completed" | "not_completed";
  assessment_status: AssessmentStatus;
  assessment_kind: "auto" | "expert" | null;
  score: number | null;
  passed: boolean | null;
  open_seconds: number | null;
  work_seconds: number | null;
  total_seconds: number | null;
  level_at_start: string;
  critical_errors: string[];
  errors: PublicError[];
  interruptions: unknown;
  full_name: string;
  workstation_no: number;
  scenario_title: string;
  ordinal: number;
  level: string;
  assessment_id: string | null;
  assessment_revision: number | null;
  // 112-6/ADR-026: set only for an operator112_intake row with a final
  // ready assessment on rubric-v2 — undefined/empty for a DDS row.
  intake_blocks?: IntakeBlockScore[];
  intake_penalty_total?: number | null;
  // ДДС-4/ADR-034: G_GRAMMAR's error count; null/undefined unless the
  // item was judged on dds/rubric-v3.
  comment_errors?: number | null;
}

export interface ReportParticipant {
  user_id: string;
  full_name: string;
  workstation_no: number;
  level: string;
  items: number;
  ready_assessments: number;
  pending_assessments: number;
  interrupted_items: number;
  avg_score: number | null;
  avg_open_seconds: number | null;
  avg_work_seconds: number | null;
  avg_total_seconds: number | null;
}

export interface LessonReport {
  lesson: { id: string; title: string; mode: string; finished_at: string };
  items: ReportItem[];
  participants: ReportParticipant[];
  aggregates: {
    avg_score: number | null;
    ready_assessments: number;
    pending_assessments: number;
    interrupted_items: number;
    score_histogram: Array<{ bucket: string; count: number }>;
    top_errors: Array<{ criterion_id: string; count: number }>;
  };
}

export interface ReportFile {
  id: string;
  lesson_id: string;
  task_id: string;
  status: "queued" | "building" | "ready" | "failed";
  requested_at: string;
  generated_at: string | null;
  download_url: string | null;
}

export interface Progress {
  user_id: string;
  level: string;
  items_total: number;
  completed_items: number;
  pending_assessments: number;
  avg_score: number | null;
  by_lesson: Array<{
    lesson_id: string;
    title: string;
    date: string;
    items: number;
    avg_score: number | null;
  }>;
  error_frequency: Array<{ criterion_id: string; count: number }>;
}

export const lessonReportQueryKey = (lessonId: string) => ["report", "lesson", lessonId] as const;
export const reportFilesQueryKey = (lessonId: string) => ["report-files", lessonId] as const;

export function useLessonReport(lessonId: string) {
  return useQuery({
    queryKey: lessonReportQueryKey(lessonId),
    queryFn: () => api.get<LessonReport>(`/lessons/${encodeURIComponent(lessonId)}/report`),
    enabled: lessonId !== "",
  });
}

export function useReportFiles(lessonId: string) {
  return useQuery({
    queryKey: reportFilesQueryKey(lessonId),
    queryFn: () => api.get<ReportFile[]>(`/lessons/${encodeURIComponent(lessonId)}/report-files`),
    enabled: lessonId !== "",
    refetchInterval: (query) => query.state.data?.some((file) => file.status === "queued" || file.status === "building") ? 2000 : false,
  });
}

export function requestLessonPDF(lessonId: string) {
  return api.post<ReportFile>(`/lessons/${encodeURIComponent(lessonId)}/report.pdf`);
}

export const myResultsQueryKey = (exerciseType: string) => ["my-results", exerciseType] as const;
export const myProgressQueryKey = (exerciseType: string) => ["my-progress", exerciseType] as const;

export function useMyResults(exerciseType: "dds_processing" | "operator112_intake" = "dds_processing") {
  return useQuery({ queryKey: myResultsQueryKey(exerciseType), queryFn: () => api.get<ReportItem[]>(`/my/results?exercise_type=${exerciseType}`) });
}

export function useMyProgress(exerciseType: "dds_processing" | "operator112_intake" = "dds_processing") {
  return useQuery({ queryKey: myProgressQueryKey(exerciseType), queryFn: () => api.get<Progress>(`/my/progress?exercise_type=${exerciseType}`) });
}
