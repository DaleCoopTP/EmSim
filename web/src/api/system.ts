import { useQuery } from "@tanstack/react-query";
import { api } from "./client";
import type { components } from "./schema";

// ADR-038: what every signed-in role may know about the system — today
// only whether the administrator has switched maintenance mode on, in
// which case no new lesson or preview can start.
export type Maintenance = components["schemas"]["Maintenance"];
export const systemQueryKey = ["system"] as const;

export function useSystem() {
  return useQuery({
    queryKey: systemQueryKey,
    queryFn: () => api.get<{ maintenance: Maintenance }>("/system"),
    refetchInterval: 30_000,
  });
}

// True while new lessons and previews are refused. Unknown (still
// loading, or the request failed) counts as off: the server refuses
// anyway, this only decides whether a button explains itself first.
export function useMaintenanceOn(): { on: boolean; reason: string } {
  const system = useSystem();
  const state = system.data?.maintenance;
  return { on: state?.enabled ?? false, reason: state?.reason ?? "" };
}
