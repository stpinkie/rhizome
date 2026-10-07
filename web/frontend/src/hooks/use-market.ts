import { useQuery } from "@tanstack/react-query"

import { getMarketSessions } from "@/api/market"

/** Polls the market sessions/disputes surface behind the Network page panel. */
export function useMarketSessions() {
  return useQuery({
    queryKey: ["market", "sessions"],
    queryFn: getMarketSessions,
    refetchInterval: 30000,
    staleTime: 5000,
    retry: 1,
  })
}
