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

export function useMyRun() {
  return useQuery({
    queryKey: myRunQueryKey,
    queryFn: () => api.get<MyRun | undefined>("/my/run"),
    refetchInterval: 2_000,
    refetchOnWindowFocus: true,
  });
}

export function useMyItems(enabled = true) {
  return useQuery({
    queryKey: myItemsQueryKey,
    queryFn: () => api.get<ItemSummary[]>("/my/items"),
    enabled,
    refetchInterval: enabled ? 2_000 : false,
    refetchOnWindowFocus: true,
  });
}

export function useItem(id: string, enabled = true) {
  return useQuery({
    queryKey: itemQueryKey(id),
    queryFn: () => api.get<Item>(`/items/${encodeURIComponent(id)}`),
    enabled: enabled && id !== "",
    refetchInterval: enabled && id !== "" ? 2_000 : false,
    refetchOnWindowFocus: true,
  });
}
