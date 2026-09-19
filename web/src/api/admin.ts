import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
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
