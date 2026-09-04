import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useCallback } from "react"

import {
  getNetworkSavedPeers,
  removeNetworkPeer,
  untrustNetworkPeer,
} from "@/api/network"

const savedPeersQueryKey = ["network", "saved-peers"]
const statusQueryKey = ["network", "status"]

export function useSavedPeers() {
  const queryClient = useQueryClient()

  const query = useQuery({
    queryKey: savedPeersQueryKey,
    queryFn: getNetworkSavedPeers,
    refetchInterval: 60000,
    staleTime: 5000,
    retry: 1,
  })

  const refresh = useCallback(() => {
    void queryClient.invalidateQueries({ queryKey: savedPeersQueryKey })
    void queryClient.invalidateQueries({ queryKey: statusQueryKey })
  }, [queryClient])

  // Refresh the saved peers list when each mutation finishes, regardless of
  // outcome, so the UI reflects any server-side changes (e.g. a peer already
  // removed elsewhere). The underlying useQuery is bounded by retry: 1 and
  // refetchInterval: 60000, so a failing backend cannot trigger an unbounded
  // refetch loop.
  const untrust = useMutation({
    mutationFn: untrustNetworkPeer,
    onSettled: () => refresh(),
  })

  const remove = useMutation({
    mutationFn: removeNetworkPeer,
    onSettled: () => refresh(),
  })

  return {
    query,
    refresh,
    untrust,
    remove,
  }
}
