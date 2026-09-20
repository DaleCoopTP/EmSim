import { useQuery } from "@tanstack/react-query";
import { api } from "./client";
import type { components } from "./schema";

export type MyRun = components["schemas"]["MyRun"];
export type ItemSummary = components["schemas"]["ItemSummary"];
export type Item = components["schemas"]["Item"];
export type CardView = components["schemas"]["CardView"];

export const myRunQueryKey = ["training", "my-run"] as const;
export const myItemsQueryKey = ["training", "my-items"] as const;
export const itemQueryKey = (id: string) => ["training", "item", id] as const;

// slice-planning.md §5/slice-4-plan.md's C10: the SSE stream (useEventStream,
// /my/stream) is now the primary invalidation signal — these queries no
// longer poll every 2s. refetchOnWindowFocus/refetchOnReconnect stay on
// as a fallback for a tab that was backgrounded through a missed/
// coalesced browser event, not as the main mechanism.
export function useMyRun() {
  return useQuery({
    queryKey: myRunQueryKey,
    // api.get maps HTTP 204 to undefined, but TanStack Query reserves
    // undefined for "the query produced no data" and treats it as an
    // error. Normalize the expected "no active run" response to null.
    queryFn: async (): Promise<MyRun | null> => (await api.get<MyRun | undefined>("/my/run")) ?? null,
    refetchOnWindowFocus: true,
    refetchOnReconnect: true,
  });
}

export function useMyItems(enabled = true) {
  return useQuery({
    queryKey: myItemsQueryKey,
    queryFn: () => api.get<ItemSummary[]>("/my/items"),
    enabled,
    refetchOnWindowFocus: true,
    refetchOnReconnect: true,
  });
}

export function useItem(id: string, enabled = true) {
  return useQuery({
    queryKey: itemQueryKey(id),
    queryFn: () => api.get<Item>(`/items/${encodeURIComponent(id)}`),
    enabled: enabled && id !== "",
    refetchOnWindowFocus: true,
    refetchOnReconnect: true,
  });
}
