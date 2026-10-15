import { useQuery } from "@tanstack/react-query"

import { getNetworkEconomy } from "@/api/network"

/** Polls the economy snapshot behind the Network page economy panel. */
export function useEconomy() {
  return useQuery({
    queryKey: ["network", "economy"],
    queryFn: getNetworkEconomy,
    refetchInterval: 30000,
    staleTime: 5000,
    retry: 1,
  })
}
