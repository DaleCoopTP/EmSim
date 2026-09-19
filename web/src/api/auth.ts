import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "./client";
import type { components, paths } from "./schema";
import { meQueryKey, type Me } from "./useMe";

export type LoginRequest = paths["/auth/login"]["post"]["requestBody"]["content"]["application/json"];
export type Role = components["schemas"]["Role"];

// On success the Me from the login response is written straight into the
// ["me"] cache — the same shape GET /me returns — so the redirect to "/"
// renders the role home without a second round trip and without a flash
// of the "not signed in" state.
export function useLogin() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: LoginRequest) => api.post<Me>("/auth/login", body),
    onSuccess: (me) => queryClient.setQueryData(meQueryKey, me),
  });
}

// Logout is 204 even without a valid session (openapi.yaml), so the
// cache is cleared unconditionally; the server has revoked the session
// by the time this resolves, and every protected query is dropped
// rather than left to fail with 401 one by one.
export function useLogout() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => api.post<void>("/auth/logout"),
    onSuccess: () => {
      queryClient.setQueryData(meQueryKey, null);
      queryClient.removeQueries({ predicate: (q) => q.queryKey[0] !== "me" });
    },
  });
}
