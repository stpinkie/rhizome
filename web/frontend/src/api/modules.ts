import { launcherFetch } from "@/api/http"

export interface ModuleConfigField {
  key: string
  label: string
  env?: string
  arg?: string
  secret?: boolean
  required?: boolean
  default?: string
}

export interface ModuleSpec {
  id: string
  name: string
  description: string
  license: string
  kind: "daemon" | "ondemand" | "config"
  platforms?: string[]
  install: {
    method: string
    repo?: string
    binary?: string
    hint?: string
    releases?: { version: string; build?: string }[]
  }
  run?: { args_template?: string[]; env?: Record<string, string> }
  health?: { type: string; target: string; method?: string }
  config_fields?: ModuleConfigField[]
  notes?: string
}

export interface ModuleInfo {
  spec: ModuleSpec
  status:
    | "unsupported"
    | "missing"
    | "unconfigured"
    | "installed"
    | "configured"
    | "stopped"
    | "running"
    | "unhealthy"
  enabled: boolean
  installed_path?: string
  version?: string
  pid?: number
  started_at?: string
  restarts?: number
  last_exit?: string
  missing_fields?: string[]
  healthy?: boolean
  fields?: Record<string, string>
  secret_keys?: string[]
}

export interface ModuleListResponse {
  modules: ModuleInfo[]
}

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await launcherFetch(path, options)
  if (!res.ok) {
    let detail = `${res.status} ${res.statusText}`
    try {
      const body = await res.text()
      const parsed = JSON.parse(body)
      detail = parsed.error ?? body.trim() ?? detail
    } catch {
      // keep default
    }
    throw new Error(detail)
  }
  return res.json() as Promise<T>
}

export async function getModules(): Promise<ModuleListResponse> {
  return request<ModuleListResponse>("/api/modules")
}

export async function getModule(id: string): Promise<ModuleInfo> {
  return request<ModuleInfo>(`/api/modules/${encodeURIComponent(id)}`)
}

export async function moduleAction(
  id: string,
  action:
    | "install"
    | "uninstall"
    | "enable"
    | "disable"
    | "start"
    | "stop"
    | "restart",
  version?: string,
): Promise<{ status: string }> {
  return request(`/api/modules/${encodeURIComponent(id)}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ action, version }),
  })
}

export async function setModuleFields(
  id: string,
  fields: Record<string, string>,
): Promise<void> {
  await request(`/api/modules/${encodeURIComponent(id)}/fields`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(fields),
  })
}

export async function setModuleSecrets(
  id: string,
  secrets: Record<string, string>,
): Promise<void> {
  await request(`/api/modules/${encodeURIComponent(id)}/secrets`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(secrets),
  })
}

export async function getModuleLogs(
  id: string,
  tail = 200,
): Promise<{ stdout: string; stderr: string }> {
  return request(`/api/modules/${encodeURIComponent(id)}/logs?tail=${tail}`)
}
