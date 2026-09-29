import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "./client";
import type { components } from "./schema";

export type User = components["schemas"]["User"];
export type UserCreate = components["schemas"]["UserCreate"];
export type UserPatch = components["schemas"]["UserPatch"];
export type Workstation = components["schemas"]["Workstation"];

export interface UserList {
  items: User[];
  total: number;
}

export const usersQueryKey = (page: number, pageSize: number) => ["admin", "users", page, pageSize] as const;
export const workstationsQueryKey = ["admin", "workstations"] as const;

export function useUsers(page: number, pageSize: number) {
  return useQuery({
    queryKey: usersQueryKey(page, pageSize),
    queryFn: () => api.get<UserList>(`/admin/users?page=${page}&page_size=${pageSize}`),
    placeholderData: (previous) => previous,
  });
}

export function useCreateUser() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: UserCreate) => api.post<User>("/admin/users", body),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["admin", "users"] }),
  });
}

export function useUpdateUser() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, patch }: { id: string; patch: UserPatch }) =>
      api.patch<User>(`/admin/users/${encodeURIComponent(id)}`, patch),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["admin", "users"] }),
  });
}

export function useWorkstations() {
  return useQuery({
    queryKey: workstationsQueryKey,
    queryFn: () => api.get<Workstation[]>("/admin/workstations"),
  });
}

// PUT /admin/workstations is "replace the list": every row sent is
// upserted by number and activated, every number not sent is
// deactivated (never deleted). The response is the full resulting list,
// so it is written into the cache directly instead of refetched.
export type WorkstationInput = Pick<Workstation, "number" | "label">;

export function useReplaceWorkstations() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: WorkstationInput[]) => api.put<Workstation[]>("/admin/workstations", body),
    onSuccess: (list) => queryClient.setQueryData(workstationsQueryKey, list),
  });
}

// ADR-033: the administrator's status screen. Polled every 10 s; the
// manual backup and the task retry refetch it at once.
export type AdminStatus = components["schemas"]["AdminStatus"];
export type AdminTaskSummary = components["schemas"]["AdminTaskSummary"];
export const statusQueryKey = ["admin", "status"] as const;

export function useAdminStatus() {
  return useQuery({
    queryKey: statusQueryKey,
    queryFn: () => api.get<AdminStatus>("/admin/status"),
    refetchInterval: 10_000,
  });
}

export function useStartBackup() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => api.post<{ task_id: string }>("/admin/backup"),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: statusQueryKey }),
  });
}

export function useRetryTask() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (taskId: string) => api.post<AdminTaskSummary>(`/admin/tasks/${encodeURIComponent(taskId)}/retry`),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: statusQueryKey }),
  });
}

// ADR-038: the audit log. Newest first; each page's next_cursor is the
// next request's `before`.
export type AuditRow = components["schemas"]["AuditRow"];
export type AuditPage = components["schemas"]["AuditPage"];

export interface AuditFilter {
  from?: string;
  to?: string;
  actorId?: string;
  action?: string;
  outcome?: string;
}

export function auditQuery(filter: AuditFilter, before?: string | null, limit?: number): string {
  const params = new URLSearchParams();
  if (filter.from) params.set("from", filter.from);
  if (filter.to) params.set("to", filter.to);
  if (filter.actorId) params.set("actor_id", filter.actorId);
  if (filter.action) params.set("action", filter.action);
  if (filter.outcome) params.set("outcome", filter.outcome);
  if (before) params.set("before", before);
  if (limit) params.set("limit", String(limit));
  const text = params.toString();
  return text ? `?${text}` : "";
}

// The CSV is a plain download: the session cookie rides on the link.
export const auditCsvUrl = (filter: AuditFilter) => `/api/v1/admin/audit.csv${auditQuery(filter)}`;

export function useAuditLog(filter: AuditFilter) {
  return useInfiniteQuery({
    queryKey: ["admin", "audit", filter] as const,
    queryFn: ({ pageParam }) => api.get<AuditPage>(`/admin/audit${auditQuery(filter, pageParam, 100)}`),
    initialPageParam: null as string | null,
    getNextPageParam: (last) => last.next_cursor,
  });
}

// ADR-038: the effective configuration, read-only. Values come from .env
// on the server; nothing here writes.
export type AdminConfig = components["schemas"]["AdminConfig"];
export type AdminConfigParam = components["schemas"]["AdminConfigParam"];

export function useAdminConfig() {
  return useQuery({
    queryKey: ["admin", "config"] as const,
    queryFn: () => api.get<AdminConfig>("/admin/config"),
    refetchInterval: 30_000,
  });
}

// ADR-038: maintenance mode. The response is the new state, so the
// banner query is refreshed at once instead of on its 30 s poll.
export function useSetMaintenance() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: { enabled: boolean; reason?: string }) => api.put<components["schemas"]["Maintenance"]>("/admin/maintenance", body),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["system"] });
      void queryClient.invalidateQueries({ queryKey: statusQueryKey });
    },
  });
}

// ADR-038: usage statistics and the failures report share one period,
// given as UTC days (usage) or instants (failures). Both have a CSV that
// is a plain download.
export type UsageReport = components["schemas"]["UsageReport"];
export type UsageDay = components["schemas"]["UsageDay"];
export type FailuresReport = components["schemas"]["FailuresReport"];

export interface ReportPeriod {
  from: string; // YYYY-MM-DD, UTC day
  to: string; // YYYY-MM-DD, UTC day, inclusive
}

const usageQuery = (p: ReportPeriod) => `?from=${p.from}&to=${p.to}`;
// The failures report takes instants: the period runs to the end of `to`.
const failuresQuery = (p: ReportPeriod) => {
  const end = new Date(`${p.to}T00:00:00Z`);
  end.setUTCDate(end.getUTCDate() + 1);
  return `?from=${p.from}T00:00:00Z&to=${end.toISOString().replace(".000Z", "Z")}`;
};

export const usageCsvUrl = (p: ReportPeriod) => `/api/v1/admin/usage.csv${usageQuery(p)}`;
export const failuresCsvUrl = (p: ReportPeriod) => `/api/v1/admin/failures.csv${failuresQuery(p)}`;

export function useUsageReport(period: ReportPeriod) {
  return useQuery({
    queryKey: ["admin", "usage", period] as const,
    queryFn: () => api.get<UsageReport>(`/admin/usage${usageQuery(period)}`),
  });
}

export function useFailuresReport(period: ReportPeriod) {
  return useQuery({
    queryKey: ["admin", "failures", period] as const,
    queryFn: () => api.get<FailuresReport>(`/admin/failures${failuresQuery(period)}`),
  });
}
