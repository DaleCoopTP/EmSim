// content module's read side the SPA needs so far (slice 2's C5):
// GET /services, so the admin Users form offers a real service_code
// instead of free text. The scenario catalogue itself (GET /scenarios*)
// is instructor UI, added in C6.
import { useQuery } from "@tanstack/react-query";
import { api } from "./client";
import type { components } from "./schema";

export type Service = components["schemas"]["Service"];

export const servicesQueryKey = ["content", "services"] as const;

export function useServices() {
  return useQuery({
    queryKey: servicesQueryKey,
    queryFn: () => api.get<Service[]>("/services"),
  });
}
