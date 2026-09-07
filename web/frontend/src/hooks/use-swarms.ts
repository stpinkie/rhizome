import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useEffect, useRef, useState } from "react"

import {
  getSwarmOffers,
  getSwarms,
  runSwarmGoal,
  submitSwarmOffer,
  swarmAction,
} from "@/api/network"

const swarmsQueryKey = ["network", "swarms"]

export function useSwarms() {
  const queryClient = useQueryClient()
  const [streamConnected, setStreamConnected] = useState(false)
  const eventSourceRef = useRef<EventSource | null>(null)

  const query = useQuery({
    queryKey: swarmsQueryKey,
    queryFn: getSwarms,
    refetchInterval: streamConnected ? false : 15000,
    staleTime: 5000,
    retry: 1,
  })

  // Live swarm events refresh the roster immediately when the daemon runs.
  useEffect(() => {
    const url = new URL(`/api/network/swarms/events`, window.location.origin)
    const es = new EventSource(url.toString())
    eventSourceRef.current = es

    es.onopen = () => setStreamConnected(true)
    es.onerror = () => setStreamConnected(false)
    es.onmessage = () => {
      void queryClient.invalidateQueries({ queryKey: swarmsQueryKey })
      void queryClient.invalidateQueries({ queryKey: ["network", "swarm-offers"] })
    }

    return () => {
      es.close()
      eventSourceRef.current = null
      setStreamConnected(false)
    }
  }, [queryClient])

  const join = useMutation({
    mutationFn: (swarmID: string) => swarmAction(swarmID, "join"),
    onSettled: () =>
      queryClient.invalidateQueries({ queryKey: swarmsQueryKey }),
  })
  const leave = useMutation({
    mutationFn: (swarmID: string) => swarmAction(swarmID, "leave"),
    onSettled: () =>
      queryClient.invalidateQueries({ queryKey: swarmsQueryKey }),
  })

  return { query, join, leave, streamConnected }
}

export function useSwarmOffers(swarmID: string | null) {
  const query = useQuery({
    queryKey: ["network", "swarm-offers", swarmID],
    queryFn: () => getSwarmOffers(swarmID as string),
    enabled: swarmID !== null && swarmID !== "",
    refetchInterval: 5000,
    retry: 1,
  })

  const offer = useMutation({
    mutationFn: (body: {
      agent_id: string
      model?: string
      task: string
      tools?: string[]
    }) => submitSwarmOffer(swarmID as string, body),
    onSettled: () => query.refetch(),
  })

  const run = useMutation({
    mutationFn: (body: { goal: string; agent_id?: string }) =>
      runSwarmGoal(swarmID as string, body.goal, body.agent_id),
    onSettled: () => query.refetch(),
  })

  return { query, offer, run }
}
