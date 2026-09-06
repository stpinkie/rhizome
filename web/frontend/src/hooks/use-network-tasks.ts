import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useCallback } from "react"

import {
  type MeshTaskSubmitRequest,
  cancelNetworkTask,
  getNetworkAudit,
  listNetworkTasks,
  submitNetworkTask,
} from "@/api/network"

const tasksQueryKey = (peer: string | null) => ["network", "tasks", peer]
const auditQueryKey = ["network", "audit"]

export function useNetworkTasks(peer: string | null) {
  const queryClient = useQueryClient()

  const query = useQuery({
    queryKey: tasksQueryKey(peer),
    queryFn: () => listNetworkTasks(peer as string),
    enabled: peer !== null && peer !== "",
    refetchInterval: 15000,
    staleTime: 2000,
    retry: 1,
  })

  const refresh = useCallback(() => {
    void queryClient.invalidateQueries({ queryKey: ["network", "tasks"] })
  }, [queryClient])

  const submit = useMutation({
    mutationFn: (body: MeshTaskSubmitRequest) => submitNetworkTask(body),
    onSettled: () => refresh(),
  })

  const cancel = useMutation({
    mutationFn: (taskID: string) => cancelNetworkTask(peer as string, taskID),
    onSettled: () => refresh(),
  })

  return { query, refresh, submit, cancel }
}

export function useNetworkAudit(tail = 50) {
  return useQuery({
    queryKey: [...auditQueryKey, tail],
    queryFn: () => getNetworkAudit(tail),
    refetchInterval: 30000,
    staleTime: 5000,
    retry: 1,
  })
}
