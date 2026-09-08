import {
  IconDownload,
  IconTrash,
  IconWorld,
} from "@tabler/icons-react"
import { useCallback, useEffect, useState } from "react"

import {
  getBrowserBackends,
  getBrowserConfig,
  installBrowserBackend,
  saveBrowserConfig,
  uninstallBrowserBackend,
  type BrowserBackend,
  type BrowserBackendConfig,
  type BrowserConfig,
} from "@/api/browser"
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

const SECRET_SENTINEL = "***"
// Values the backend returns for masked SecureString fields ("[NOT_HERE]") or
// the UI sentinel — treat both as "a secret is stored, field left untouched".
const MASKED_SECRET_VALUES = new Set([SECRET_SENTINEL, "[NOT_HERE]"])

function isMaskedSecret(v: string | undefined): boolean {
  return v != null && MASKED_SECRET_VALUES.has(v)
}

function stateBadge(state: string) {
  switch (state) {
    case "installed":
    case "configured":
      return <Badge variant="default">{state}</Badge>
    case "missing":
    case "unconfigured":
      return <Badge variant="secondary">{state}</Badge>
    default:
      return <Badge variant="outline">{state}</Badge>
  }
}

export function BrowserPage() {
  const [enabled, setEnabled] = useState(false)
  const [defaultBackend, setDefaultBackend] = useState("agent-browser")
  const [sessionTimeout, setSessionTimeout] = useState("")
  const [privateHostWhitelist, setPrivateHostWhitelist] = useState<string[]>([])
  const [backends, setBackends] = useState<BrowserBackend[]>([])
  const [backendCfgs, setBackendCfgs] = useState<
    Record<string, BrowserBackendConfig>
  >({})
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [busyBackend, setBusyBackend] = useState<string | null>(null)
  const [message, setMessage] = useState<string | null>(null)

  const refresh = useCallback(async () => {
    try {
      const [list, cfg] = await Promise.all([
        getBrowserBackends(),
        getBrowserConfig(),
      ])
      setBackends(list.backends)
      setEnabled(list.enabled)
      setDefaultBackend(list.default_backend || "agent-browser")
      setSessionTimeout(cfg.session_timeout ?? "")
      setPrivateHostWhitelist(cfg.private_host_whitelist ?? [])
      setBackendCfgs(cfg.backends ?? {})
      setMessage(null)
    } catch (err) {
      setMessage(err instanceof Error ? err.message : String(err))
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void refresh()
  }, [refresh])

  const save = useCallback(
    async (next: {
      enabled?: boolean
      defaultBackend?: string
      backends?: Record<string, BrowserBackendConfig>
    }) => {
      setSaving(true)
      setMessage(null)
      try {
        // Always echo back the fields this page doesn't edit so a save
        // never drops session_timeout / private_host_whitelist configured
        // directly in config.json.
        const payload: BrowserConfig = {
          enabled: next.enabled ?? enabled,
          default_backend: next.defaultBackend ?? defaultBackend,
          session_timeout: sessionTimeout || undefined,
          private_host_whitelist: privateHostWhitelist,
          backends: next.backends ?? backendCfgs,
        }
        await saveBrowserConfig(payload)
        await refresh()
      } catch (err) {
        setMessage(err instanceof Error ? err.message : String(err))
      } finally {
        setSaving(false)
      }
    },
    [enabled, defaultBackend, sessionTimeout, privateHostWhitelist, backendCfgs, refresh],
  )

  const setField = (id: string, key: string, value: string) => {
    setBackendCfgs((prev) => ({
      ...prev,
      [id]: { ...prev[id], [key]: value },
    }))
  }

  const saveBackend = () => {
    void save({ backends: backendCfgs })
  }

  const doInstall = async (id: string) => {
    setBusyBackend(id)
    setMessage(null)
    try {
      const res = await installBrowserBackend(id)
      setMessage(res.message)
      await refresh()
    } catch (err) {
      setMessage(err instanceof Error ? err.message : String(err))
    } finally {
      setBusyBackend(null)
    }
  }

  const doUninstall = async (id: string) => {
    setBusyBackend(id)
    setMessage(null)
    try {
      const res = await uninstallBrowserBackend(id)
      setMessage(res.message)
      await refresh()
    } catch (err) {
      setMessage(err instanceof Error ? err.message : String(err))
    } finally {
      setBusyBackend(null)
    }
  }

  return (
    <div className="flex h-full flex-col">
      <PageHeader
        title="Browser Automation"
        titleExtra={
          <div className="flex items-center gap-2">
            <Label htmlFor="browser-enabled" className="text-muted-foreground text-xs">
              {enabled ? "Enabled" : "Disabled"}
            </Label>
            <Switch
              id="browser-enabled"
              checked={enabled}
              disabled={saving}
              onCheckedChange={(v) => void save({ enabled: v })}
            />
          </div>
        }
      />

      <div className="flex-1 overflow-y-auto p-4 sm:p-8">
        {message && (
          <p className="text-muted-foreground mb-4 text-sm">{message}</p>
        )}
        {loading ? (
          <p className="text-muted-foreground text-sm">Loading…</p>
        ) : (
          <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
            {backends.map((b) => {
              const cfg = backendCfgs[b.id] ?? {}
              return (
                <Card key={b.id} className="flex flex-col">
                  <CardHeader className="pb-3">
                    <div className="flex items-start justify-between gap-2">
                      <div>
                        <CardTitle className="flex items-center gap-2 text-base">
                          <IconWorld className="size-4 opacity-60" />
                          {b.name}
                          {b.is_default && (
                            <Badge variant="outline">default</Badge>
                          )}
                        </CardTitle>
                        <CardDescription className="mt-1 text-xs">
                          {b.kind} · {b.license}
                        </CardDescription>
                      </div>
                      {stateBadge(b.state)}
                    </div>
                  </CardHeader>
                  <CardContent className="flex flex-1 flex-col gap-3 pt-0">
                    <p className="text-muted-foreground text-xs">{b.notes}</p>
                    {b.version && (
                      <p className="text-muted-foreground text-xs">
                        installed: {b.version}
                      </p>
                    )}
                    {b.free_space_msg && (
                      <p className="text-xs text-amber-600 dark:text-amber-400">
                        {b.free_space_msg}
                      </p>
                    )}
                    {b.driver_ready === false && (
                      <p className="text-xs text-amber-600 dark:text-amber-400">
                        Requires the agent-browser CLI — install the
                        agent-browser backend first.
                      </p>
                    )}

                    {b.auth.length > 0 && (
                      <div className="flex flex-col gap-2">
                        {b.auth.map((f) => {
                          const rawVal = (
                            cfg as Record<string, string | undefined>
                          )[f.key]
                          return (
                          <div key={f.key} className="grid gap-1">
                            <Label className="text-xs">
                              {f.label}
                              {f.required && (
                                <span className="text-destructive"> *</span>
                              )}
                            </Label>
                            {f.key === "env" ? (
                              <Input
                                // Remount when the stored env map changes so
                                // the uncontrolled input reflects refreshes.
                                key={`env-${b.id}-${JSON.stringify(cfg.env ?? null)}`}
                                placeholder='{"KEY":"value"}'
                                className="h-8 text-xs"
                                defaultValue={
                                  cfg.env ? JSON.stringify(cfg.env) : ""
                                }
                                onBlur={(e) => {
                                  const v = e.target.value.trim()
                                  if (v === "") {
                                    // Clearing must remove the key entirely —
                                    // sending "" would fail to unmarshal into
                                    // the backend's map[string]string field.
                                    setBackendCfgs((prev) => {
                                      const entry = { ...prev[b.id] }
                                      delete entry.env
                                      return { ...prev, [b.id]: entry }
                                    })
                                    return
                                  }
                                  try {
                                    const parsed = JSON.parse(v)
                                    setBackendCfgs((prev) => ({
                                      ...prev,
                                      [b.id]: { ...prev[b.id], env: parsed },
                                    }))
                                  } catch {
                                    setMessage(`Invalid JSON in env for ${b.id}`)
                                  }
                                }}
                              />
                            ) : (
                              <Input
                                type={f.secret ? "password" : "text"}
                                placeholder={
                                  f.secret && isMaskedSecret(rawVal)
                                    ? SECRET_SENTINEL
                                    : undefined
                                }
                                className="h-8 text-xs"
                                value={isMaskedSecret(rawVal) ? "" : rawVal ?? ""}
                                onChange={(e) =>
                                  setField(b.id, f.key, e.target.value)
                                }
                              />
                            )}
                          </div>
                          )
                        })}
                        <Button
                          size="sm"
                          variant="secondary"
                          className="mt-1 self-start"
                          disabled={saving}
                          onClick={() => saveBackend()}
                        >
                          Save credentials
                        </Button>
                      </div>
                    )}

                    <div className="mt-auto flex items-center gap-2 pt-2">
                      {b.install.method === "npm" &&
                        (b.state === "installed" ? (
                          <Button
                            size="sm"
                            variant="outline"
                            disabled={busyBackend === b.id}
                            onClick={() => void doUninstall(b.id)}
                          >
                            <IconTrash className="size-4" />
                            Uninstall
                          </Button>
                        ) : (
                          <Button
                            size="sm"
                            disabled={
                              busyBackend === b.id || b.free_space_ok === false
                            }
                            onClick={() => void doInstall(b.id)}
                          >
                            <IconDownload className="size-4" />
                            {busyBackend === b.id ? "Installing…" : "Install"}
                          </Button>
                        ))}
                      {b.install.hint && b.install.method !== "npm" && (
                        <p className="text-muted-foreground text-xs">
                          {b.install.hint}
                        </p>
                      )}
                      {!b.is_default && (
                        <Button
                          size="sm"
                          variant="ghost"
                          className="ml-auto text-xs"
                          disabled={saving}
                          onClick={() => void save({ defaultBackend: b.id })}
                        >
                          Set default
                        </Button>
                      )}
                    </div>
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
