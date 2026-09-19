import { useQuery } from "@tanstack/react-query";
import { ApiError, api } from "./client";
import type { components } from "./schema";

export type Me = components["schemas"]["Me"];

export const meQueryKey = ["me"] as const;

// useMe reads GET /me. A 401 is the expected "not signed in" shape, not
// a fetch failure: it resolves to null so a route can redirect to
// /login itself, instead of react-query surfacing an error state for
// something that happens on every fresh visit.
export function useMe() {
  return useQuery({
    queryKey: meQueryKey,
    queryFn: async (): Promise<Me | null> => {
      try {
        return await api.get<Me>("/me");
      } catch (error) {
        if (error instanceof ApiError && error.status === 401) {
          return null;
        }
        throw error;
      }
    },
    retry: false,
  });
}
