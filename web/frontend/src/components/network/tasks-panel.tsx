import {
  IconCircleX,
  IconLoader2,
  IconPlayerPlay,
  IconRefresh,
} from "@tabler/icons-react"
import { useMemo, useState } from "react"
import { useTranslation } from "react-i18next"

import type { MeshTaskInfo, SavedPeer } from "@/api/network"
import { getNetworkTask } from "@/api/network"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import { useNetworkTasks } from "@/hooks/use-network-tasks"

const TERMINAL_STATUSES = new Set([
  "done",
  "error",
  "cancelled",
  "not_found",
  "rejected",
])

function statusVariant(
  status: string,
): "default" | "secondary" | "outline" | "destructive" {
  switch (status) {
    case "done":
      return "default"
    case "running":
    case "accepted":
      return "secondary"
    case "error":
    case "rejected":
      return "destructive"
    default:
      return "outline"
  }
}

interface TaskResultState {
  loading?: boolean
  text?: string
  error?: string
}

interface TasksPanelProps {
  peers: SavedPeer[]
}

export function TasksPanel({ peers }: TasksPanelProps) {
  const { t } = useTranslation()
  const [selectedPeer, setSelectedPeer] = useState<string | null>(null)
  const [agentID, setAgentID] = useState("main")
  const [model, setModel] = useState("")
  const [taskText, setTaskText] = useState("")
  const [submitError, setSubmitError] = useState<string | null>(null)
  const [results, setResults] = useState<Record<string, TaskResultState>>({})

  const { query, refresh, submit, cancel } = useNetworkTasks(selectedPeer)

  const peer = useMemo(
    () => peers.find((p) => p.peer_id === selectedPeer),
    [peers, selectedPeer],
  )

  const tasks = useMemo(
    () =>
      (query.data?.tasks ?? [])
        .slice()
        .sort((a, b) => (b.created_at ?? "").localeCompare(a.created_at ?? "")),
    [query.data],
  )

  const onSubmit = () => {
    if (!selectedPeer || taskText.trim() === "") return
    setSubmitError(null)
    submit.mutate(
      {
        peer: selectedPeer,
        agent_id: agentID.trim() || "main",
        model: model.trim() || undefined,
        task: taskText.trim(),
      },
      {
        onSuccess: () => setTaskText(""),
        onError: (err) =>
          setSubmitError(err instanceof Error ? err.message : String(err)),
      },
    )
  }

  const fetchResult = async (taskID: string) => {
    if (!selectedPeer) return
    setResults((r) => ({ ...r, [taskID]: { loading: true } }))
    try {
      const resp = await getNetworkTask(selectedPeer, taskID, "30s")
      const text =
        resp.result?.for_user || resp.result?.for_llm || resp.error || ""
      setResults((r) => ({
        ...r,
        [taskID]: { text: text || `(${resp.status})` },
      }))
    } catch (err) {
      setResults((r) => ({
        ...r,
        [taskID]: { error: err instanceof Error ? err.message : String(err) },
      }))
    }
  }

  return (
    <Card>
      <CardHeader>
        <div className="flex items-start justify-between gap-2">
          <div>
            <CardTitle>{t("pages.network.tasks", "Remote tasks")}</CardTitle>
            <CardDescription>
              {t(
                "pages.network.tasks_description",
                "Submit and inspect asynchronous agent tasks on trusted mesh peers.",
              )}
            </CardDescription>
          </div>
          {selectedPeer && (
            <Button
              variant="outline"
              size="sm"
              onClick={() => refresh()}
              disabled={query.isFetching}
            >
              <IconRefresh
                className={`size-4 ${query.isFetching ? "animate-spin" : ""}`}
              />
            </Button>
          )}
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="space-y-2">
          <Label>{t("pages.network.task_peer", "Peer")}</Label>
          <Select
            value={selectedPeer ?? ""}
            onValueChange={(v) => setSelectedPeer(v || null)}
          >
            <SelectTrigger>
              <SelectValue
                placeholder={t(
                  "pages.network.task_peer_placeholder",
                  "Select a saved peer…",
                )}
              />
            </SelectTrigger>
            <SelectContent>
              {peers.map((p) => (
                <SelectItem key={p.peer_id} value={p.peer_id}>
                  <span className="font-mono text-xs">{p.peer_id}</span>
                  {p.trusted ? (
                    <span className="text-muted-foreground ml-2 text-xs">
                      {t("pages.network.trusted", "Trusted")}
                    </span>
                  ) : null}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          {peers.length === 0 && (
            <p className="text-muted-foreground text-xs">
              {t(
                "pages.network.tasks_no_peers",
                "No saved peers. Add a bootstrap peer first.",
              )}
            </p>
          )}
        </div>

        {selectedPeer && (
          <>
            <div className="bg-muted/40 space-y-3 rounded-lg p-4">
              <div className="grid gap-3 sm:grid-cols-2">
                <div className="space-y-1.5">
                  <Label htmlFor="task-agent">
                    {t("pages.network.task_agent", "Agent")}
                  </Label>
                  <Input
                    id="task-agent"
                    value={agentID}
                    onChange={(e) => setAgentID(e.target.value)}
                    placeholder="main"
                    list="task-agent-options"
                  />
                  <datalist id="task-agent-options">
                    {(peer?.capability?.agents ?? []).map((a) => (
                      <option key={a} value={a} />
                    ))}
                  </datalist>
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="task-model">
                    {t("pages.network.task_model", "Model (optional)")}
                  </Label>
                  <Input
                    id="task-model"
                    value={model}
                    onChange={(e) => setModel(e.target.value)}
                    placeholder={t(
                      "pages.network.task_model_placeholder",
                      "Peer's default",
                    )}
                    list="task-model-options"
                  />
                  <datalist id="task-model-options">
                    {(peer?.capability?.models ?? []).map((m) => (
                      <option key={m} value={m} />
                    ))}
                  </datalist>
                </div>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="task-prompt">
                  {t("pages.network.task_prompt", "Task")}
                </Label>
                <Textarea
                  id="task-prompt"
                  value={taskText}
                  onChange={(e) => setTaskText(e.target.value)}
                  placeholder={t(
                    "pages.network.task_prompt_placeholder",
                    "Describe the task for the remote agent…",
                  )}
                  rows={3}
                />
              </div>
              <div className="flex items-center gap-3">
                <Button
                  size="sm"
                  onClick={onSubmit}
                  disabled={submit.isPending || taskText.trim() === ""}
                >
                  {submit.isPending ? (
                    <IconLoader2 className="mr-1 size-4 animate-spin" />
                  ) : (
                    <IconPlayerPlay className="mr-1 size-4" />
                  )}
                  {t("pages.network.task_submit", "Submit task")}
                </Button>
                {submitError && (
                  <p className="text-destructive text-xs">{submitError}</p>
                )}
              </div>
            </div>

            {query.isLoading ? (
              <p className="text-muted-foreground text-sm">
                {t("pages.network.tasks_loading", "Loading tasks…")}
              </p>
            ) : query.error ? (
              <p className="text-destructive text-sm">
                {query.error instanceof Error
                  ? query.error.message
                  : String(query.error)}
              </p>
            ) : tasks.length === 0 ? (
              <p className="text-muted-foreground text-sm">
                {t(
                  "pages.network.tasks_empty",
                  "No tasks submitted to this peer yet.",
                )}
              </p>
            ) : (
              <div className="space-y-2">
                {tasks.map((task: MeshTaskInfo) => (
                  <div
                    key={task.task_id}
                    className="bg-muted/40 rounded-lg p-3"
                  >
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="font-mono text-xs break-all">
                        {task.task_id}
                      </span>
                      <Badge variant={statusVariant(task.status)}>
                        {task.status}
                      </Badge>
                      {task.agent_id && (
                        <span className="text-muted-foreground text-xs">
                          {task.agent_id}
                        </span>
                      )}
                      <div className="ml-auto flex items-center gap-1">
                        {!TERMINAL_STATUSES.has(task.status) && (
                          <Button
                            variant="ghost"
                            size="sm"
                            disabled={cancel.isPending}
                            onClick={() => cancel.mutate(task.task_id)}
                          >
                            <IconCircleX className="mr-1 size-4" />
                            {t("pages.network.task_cancel", "Cancel")}
                          </Button>
                        )}
                        <Button
                          variant="ghost"
                          size="sm"
                          disabled={results[task.task_id]?.loading}
                          onClick={() => void fetchResult(task.task_id)}
                        >
                          {results[task.task_id]?.loading ? (
                            <IconLoader2 className="mr-1 size-4 animate-spin" />
                          ) : null}
                          {t("pages.network.task_result", "Result")}
                        </Button>
                      </div>
                    </div>
                    {task.error && (
                      <p className="text-destructive mt-1 text-xs">
                        {task.error}
                      </p>
                    )}
                    {results[task.task_id]?.text && (
                      <pre className="bg-background/60 mt-2 max-h-64 overflow-auto rounded p-2 text-xs whitespace-pre-wrap">
                        {results[task.task_id].text}
                      </pre>
                    )}
                    {results[task.task_id]?.error && (
                      <p className="text-destructive mt-1 text-xs">
                        {results[task.task_id].error}
                      </p>
                    )}
                  </div>
                ))}
              </div>
            )}
          </>
        )}
      </CardContent>
    </Card>
  )
}
