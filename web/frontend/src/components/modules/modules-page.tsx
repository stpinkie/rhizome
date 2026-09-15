import {
  IconDownload,
  IconLoader2,
  IconPlayerPlay,
  IconPlayerStop,
  IconPuzzle,
  IconRefresh,
  IconRotate,
  IconTrash,
} from "@tabler/icons-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useState } from "react"
import { useTranslation } from "react-i18next"

import {
  type ModuleInfo,
  getModuleLogs,
  getModules,
  moduleAction,
  setModuleFields,
  setModuleSecrets,
} from "@/api/modules"
import { PageHeader } from "@/components/page-header"
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
import { Switch } from "@/components/ui/switch"

function statusBadge(status: ModuleInfo["status"]) {
  switch (status) {
    case "running":
      return <Badge variant="default">running</Badge>
    case "unhealthy":
      return <Badge variant="destructive">unhealthy</Badge>
    case "configured":
    case "installed":
      return <Badge variant="default">{status}</Badge>
    case "missing":
    case "unconfigured":
    case "stopped":
      return <Badge variant="secondary">{status}</Badge>
    default:
      return <Badge variant="outline">{status}</Badge>
  }
}

const KIND_LABEL: Record<string, string> = {
  daemon: "daemon",
  ondemand: "on-demand",
  config: "endpoint",
}

export function ModulesPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [drafts, setDrafts] = useState<Record<string, Record<string, string>>>(
    {},
  )
  const [logsFor, setLogsFor] = useState<string | null>(null)
  const [logs, setLogs] = useState<{ stdout: string; stderr: string } | null>(
    null,
  )
  const [message, setMessage] = useState<string | null>(null)

  const listQuery = useQuery({
    queryKey: ["modules", "list"],
    queryFn: getModules,
    refetchInterval: 15000,
    staleTime: 5000,
    retry: 1,
  })

  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: ["modules"] })

  const actionMut = useMutation({
    mutationFn: ({
      id,
      action,
    }: {
      id: string
      action:
        | "install"
        | "uninstall"
        | "enable"
        | "disable"
        | "start"
        | "stop"
        | "restart"
    }) => moduleAction(id, action),
    onSuccess: (_d, v) => {
      setMessage(null)
      void invalidate()
      if (v.action === "stop" || v.action === "restart") setLogsFor(null)
    },
    onError: (err) =>
      setMessage(err instanceof Error ? err.message : String(err)),
  })

  const saveMut = useMutation({
    mutationFn: async (info: ModuleInfo) => {
      const draft = drafts[info.spec.id] ?? {}
      const fields: Record<string, string> = {}
      const secrets: Record<string, string> = {}
      for (const f of info.spec.config_fields ?? []) {
        const v = draft[f.key]
        if (v === undefined) continue
        if (f.secret) secrets[f.key] = v
        else fields[f.key] = v
      }
      if (Object.keys(fields).length > 0)
        await setModuleFields(info.spec.id, fields)
      if (Object.keys(secrets).length > 0)
        await setModuleSecrets(info.spec.id, secrets)
    },
    onSuccess: (_d, info) => {
      setMessage(null)
      setDrafts((prev) => {
        const next = { ...prev }
        delete next[info.spec.id]
        return next
      })
      void invalidate()
    },
    onError: (err) =>
      setMessage(err instanceof Error ? err.message : String(err)),
  })

  const busy = actionMut.isPending || saveMut.isPending

  const showLogs = async (id: string) => {
    if (logsFor === id) {
      setLogsFor(null)
      setLogs(null)
      return
    }
    setLogsFor(id)
    setLogs(null)
    try {
      setLogs(await getModuleLogs(id))
    } catch (err) {
      setLogs({
        stdout: "",
        stderr: err instanceof Error ? err.message : String(err),
      })
    }
  }

  const modules = listQuery.data?.modules ?? []

  return (
    <div className="flex h-full flex-col">
      <PageHeader
        title={t("navigation.modules", "Modules")}
        children={
          <Button
            variant="outline"
            size="sm"
            onClick={() => void listQuery.refetch()}
            disabled={listQuery.isFetching}
          >
            {listQuery.isFetching ? (
              <IconLoader2 className="size-4 animate-spin" />
            ) : (
              <IconRefresh className="size-4" />
            )}
            {t("pages.network.refresh", "Refresh")}
          </Button>
        }
      />

      <div className="flex-1 overflow-y-auto p-4 sm:p-8">
        {message && <p className="text-destructive mb-4 text-sm">{message}</p>}
        {listQuery.error && (
          <p className="text-destructive mb-4 text-sm">
            {listQuery.error instanceof Error
              ? listQuery.error.message
              : String(listQuery.error)}
          </p>
        )}
        {listQuery.isLoading ? (
          <p className="text-muted-foreground text-sm">Loading…</p>
        ) : (
          <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
            {modules.map((m) => {
              const spec = m.spec
              const draft = drafts[spec.id] ?? {}
              const canInstall =
                spec.install.method === "github-release" &&
                (m.status === "missing" || m.status === "unconfigured") &&
                (spec.install.releases?.length ?? 0) > 0
              const isProcess = spec.kind !== "config"
              return (
                <Card key={spec.id} className="flex flex-col">
                  <CardHeader className="pb-3">
                    <div className="flex items-start justify-between gap-2">
                      <div>
                        <CardTitle className="flex items-center gap-2 text-base">
                          <IconPuzzle className="size-4 opacity-60" />
                          {spec.name}
                        </CardTitle>
                        <CardDescription className="mt-1 text-xs">
                          {KIND_LABEL[spec.kind] ?? spec.kind} · {spec.license}
                          {m.version ? ` · v${m.version}` : ""}
                        </CardDescription>
                      </div>
                      {statusBadge(m.status)}
                    </div>
                  </CardHeader>
                  <CardContent className="flex flex-1 flex-col gap-3 pt-0">
                    <p className="text-muted-foreground text-xs">
                      {spec.description}
                    </p>
                    {spec.notes && (
                      <p className="text-muted-foreground text-xs italic">
                        {spec.notes}
                      </p>
                    )}
                    {m.pid !== undefined && m.pid > 0 && (
                      <p className="text-muted-foreground text-xs">
                        pid {m.pid}
                        {m.restarts ? ` · ${m.restarts} restarts` : ""}
                      </p>
                    )}
                    {m.last_exit && (
                      <p className="text-xs text-amber-600 dark:text-amber-400">
                        last exit: {m.last_exit}
                      </p>
                    )}
                    {(m.missing_fields?.length ?? 0) > 0 && (
                      <p className="text-xs text-amber-600 dark:text-amber-400">
                        missing required: {m.missing_fields?.join(", ")}
                      </p>
                    )}

                    {(spec.config_fields?.length ?? 0) > 0 && (
                      <div className="flex flex-col gap-2">
                        {spec.config_fields!.map((f) => {
                          const isSecretSet =
                            f.secret && (m.secret_keys ?? []).includes(f.key)
                          return (
                            <div key={f.key} className="grid gap-1">
                              <Label className="text-xs">
                                {f.label}
                                {f.required && (
                                  <span className="text-destructive"> *</span>
                                )}
                              </Label>
                              {f.flag ? (
                                <div className="flex h-8 items-center">
                                  <input
                                    type="checkbox"
                                    className="accent-primary h-4 w-4"
                                    checked={
                                      (draft[f.key] ??
                                        m.fields?.[f.key] ??
                                        f.default ??
                                        "false") === "true"
                                    }
                                    onChange={(e) =>
                                      setDrafts((prev) => ({
                                        ...prev,
                                        [spec.id]: {
                                          ...prev[spec.id],
                                          [f.key]: e.target.checked
                                            ? "true"
                                            : "false",
                                        },
                                      }))
                                    }
                                  />
                                </div>
                              ) : (
                                <Input
                                  type={f.secret ? "password" : "text"}
                                  className="h-8 text-xs"
                                  placeholder={
                                    f.secret
                                      ? isSecretSet
                                        ? "(set)"
                                        : "(unset)"
                                      : (m.fields?.[f.key] ?? f.default ?? "")
                                  }
                                  value={
                                    draft[f.key] ??
                                    (f.secret ? "" : (m.fields?.[f.key] ?? ""))
                                  }
                                  onChange={(e) =>
                                    setDrafts((prev) => ({
                                      ...prev,
                                      [spec.id]: {
                                        ...prev[spec.id],
                                        [f.key]: e.target.value,
                                      },
                                    }))
                                  }
                                />
                              )}
                            </div>
                          )
                        })}
                        {Object.keys(draft).length > 0 && (
                          <Button
                            size="sm"
                            variant="secondary"
                            className="mt-1 self-start"
                            disabled={busy}
                            onClick={() => saveMut.mutate(m)}
                          >
                            Save fields
                          </Button>
                        )}
                      </div>
                    )}

                    <div className="mt-auto flex flex-wrap items-center gap-2 pt-2">
                      {spec.install.method === "github-release" &&
                        (m.status === "installed" ||
                        m.status === "stopped" ||
                        m.status === "running" ||
                        m.status === "unhealthy" ? (
                          <Button
                            size="sm"
                            variant="outline"
                            disabled={busy || m.status === "running"}
                            onClick={() =>
                              actionMut.mutate({
                                id: spec.id,
                                action: "uninstall",
                              })
                            }
                          >
                            <IconTrash className="size-4" />
                            Uninstall
                          </Button>
                        ) : canInstall ? (
                          <Button
                            size="sm"
                            disabled={busy}
                            onClick={() =>
                              actionMut.mutate({
                                id: spec.id,
                                action: "install",
                              })
                            }
                          >
                            <IconDownload className="size-4" />
                            Install
                          </Button>
                        ) : null)}

                      {isProcess && (
                        <>
                          {m.status === "running" ||
                          m.status === "unhealthy" ? (
                            <>
                              <Button
                                size="sm"
                                variant="outline"
                                disabled={busy}
                                onClick={() =>
                                  actionMut.mutate({
                                    id: spec.id,
                                    action: "stop",
                                  })
                                }
                              >
                                <IconPlayerStop className="size-4" />
                                Stop
                              </Button>
                              <Button
                                size="sm"
                                variant="ghost"
                                disabled={busy}
                                onClick={() =>
                                  actionMut.mutate({
                                    id: spec.id,
                                    action: "restart",
                                  })
                                }
                              >
                                <IconRotate className="size-4" />
                              </Button>
                            </>
                          ) : (
                            <Button
                              size="sm"
                              variant="outline"
                              disabled={
                                busy ||
                                m.status === "missing" ||
                                m.status === "unconfigured" ||
                                m.status === "unsupported"
                              }
                              onClick={() =>
                                actionMut.mutate({
                                  id: spec.id,
                                  action: "start",
                                })
                              }
                            >
                              <IconPlayerPlay className="size-4" />
                              Start
                            </Button>
                          )}
                          {(m.status === "running" ||
                            m.status === "unhealthy" ||
                            m.status === "stopped" ||
                            m.last_exit) && (
                            <Button
                              size="sm"
                              variant="ghost"
                              className="text-xs"
                              onClick={() => void showLogs(spec.id)}
                            >
                              {logsFor === spec.id ? "Hide logs" : "Logs"}
                            </Button>
                          )}
                        </>
                      )}

                      <div className="ml-auto flex items-center gap-2">
                        <Label
                          htmlFor={`mod-${spec.id}`}
                          className="text-muted-foreground text-xs"
                        >
                          {m.enabled ? "enabled" : "disabled"}
                        </Label>
                        <Switch
                          id={`mod-${spec.id}`}
                          checked={m.enabled}
                          disabled={
                            busy ||
                            m.status === "unsupported" ||
                            m.status === "missing" ||
                            m.status === "unconfigured"
                          }
                          onCheckedChange={(v) =>
                            actionMut.mutate({
                              id: spec.id,
                              action: v ? "enable" : "disable",
                            })
                          }
                        />
                      </div>
                    </div>

                    {logsFor === spec.id && (
                      <pre className="bg-muted mt-2 max-h-48 overflow-auto rounded-md p-2 text-[10px] leading-4">
                        {logs === null
                          ? "Loading…"
                          : (logs.stdout || "(no stdout)") +
                            (logs.stderr
                              ? `\n── stderr ──\n${logs.stderr}`
                              : "")}
                      </pre>
                    )}
                  </CardContent>
                </Card>
              )
            })}
          </div>
        )}
      </div>
    </div>
  )
}
