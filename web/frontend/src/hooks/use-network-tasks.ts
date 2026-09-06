import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useCallback, useEffect, useRef, useState } from "react"
import { toast } from "sonner"

import {
  type MeshTaskInfo,
  type MeshTaskListResponse,
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
  const [streamConnected, setStreamConnected] = useState(false)
  const eventSourceRef = useRef<EventSource | null>(null)

  const query = useQuery({
    queryKey: tasksQueryKey(peer),
    queryFn: () => listNetworkTasks(peer as string),
    enabled: peer !== null && peer !== "",
    refetchInterval: streamConnected ? false : 3000,
    staleTime: 2000,
    retry: 1,
  })

  useEffect(() => {
    if (!peer) {
      setStreamConnected(false)
      return
    }

    const url = new URL(`/api/network/tasks/events`, window.location.origin)
    url.searchParams.set("peer", peer)
    const es = new EventSource(url.toString())
    eventSourceRef.current = es

    es.onopen = () => {
      setStreamConnected(true)
    }

    es.onerror = () => {
      setStreamConnected(false)
    }

    es.onmessage = (event) => {
      try {
        const payload = JSON.parse(event.data)
        const taskID =
          typeof payload.task_id === "string" ? payload.task_id : undefined
        const agentID =
          typeof payload.agent_id === "string" ? payload.agent_id : undefined

        // Optimistically patch the cached task list when we have a task id.
        if (taskID) {
          const status =
            typeof payload.status === "string" ? payload.status : undefined
          if (status === "done" || status === "error" || status === "cancelled") {
            const agentID =
              typeof payload.agent_id === "string" ? payload.agent_id : peer
            const taskErr =
              typeof payload.error === "string" ? payload.error : undefined
            if (status === "done") {
              toast.success(`Task ${taskID.slice(-8)} on ${agentID} completed`)
            } else if (status === "error") {
              toast.error(
                `Task ${taskID.slice(-8)} on ${agentID} failed${taskErr ? `: ${taskErr}` : ""}`,
              )
            } else {
              toast.warning(`Task ${taskID.slice(-8)} on ${agentID} cancelled`)
            }
          }
          queryClient.setQueryData(
            tasksQueryKey(peer),
            (old: MeshTaskListResponse | undefined) => {
              if (!old?.tasks) {
                // Not enough data to patch; refresh from the server.
                return old
              }
              const next: MeshTaskListResponse = {
                ...old,
                tasks: [...old.tasks],
              }
              const idx = next.tasks.findIndex((t) => t.task_id === taskID)
              if (idx >= 0) {
                const existing = next.tasks[idx]
                const patch: MeshTaskInfo = {
                  ...existing,
                  status:
                    typeof payload.status === "string"
                      ? payload.status
                      : existing.status,
                  error:
                    typeof payload.error === "string"
                      ? payload.error
                      : existing.error,
                }
                if (agentID) {
                  patch.agent_id = agentID
                }
                next.tasks[idx] = patch
              } else if (agentID) {
                // New task submitted by or routed to the remote peer; append a
                // placeholder and refetch to get full metadata.
                next.tasks.push({
                  task_id: taskID,
                  agent_id: agentID,
                  status:
                    typeof payload.status === "string"
                      ? payload.status
                      : "unknown",
                })
              }
              return next
            },
          )
          // Always background-refetch to reconcile full state and timing fields.
          void queryClient.invalidateQueries({
            queryKey: tasksQueryKey(peer),
            exact: true,
            refetchType: "active",
          })
        }
      } catch {
        // Ignore malformed SSE payloads.
      }
    }

    return () => {
      es.close()
      eventSourceRef.current = null
    }
  }, [peer, queryClient])

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

  return { query, refresh, submit, cancel, streamConnected }
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
