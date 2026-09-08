import { launcherFetch } from "@/api/http"

export interface BrowserAuthField {
  key: string
  label: string
  env?: string
  secret?: boolean
  required?: boolean
}

export interface BrowserBackend {
  id: string
  name: string
  kind: string
  license: string
  status: string
  capabilities: string[]
  auth: BrowserAuthField[]
  install: { method: string; binary?: string; hint?: string }
  disk_estimate_mb: number
  notes: string
  provider?: string
  // runtime status
  state: string
  configured: boolean
  is_default: boolean
  driver_ready?: boolean // agent-browser CLI on PATH (absent for REST-only backends)
  version?: string
  free_space_ok?: boolean
  free_space_msg?: string
}

export interface BrowserBackendsResponse {
  enabled: boolean
  default_backend: string
  backends: BrowserBackend[]
}

export interface BrowserBackendConfig {
  api_key?: string
  account_id?: string
  project_id?: string
  base_url?: string
  endpoint_url?: string
  executable_path?: string
  session_name?: string
  provider?: string
  env?: Record<string, string>
}

export interface BrowserConfig {
  enabled: boolean
  default_backend?: string
  session_timeout?: string
  private_host_whitelist?: string[]
  backends?: Record<string, BrowserBackendConfig>
}

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await launcherFetch(path, options)
  if (!res.ok) {
    let detail = `${res.status} ${res.statusText}`
    try {
      detail = (await res.text()).trim() || detail
    } catch {
      // keep default
    }
    throw new Error(detail)
  }
  return res.json() as Promise<T>
}

export async function getBrowserBackends(): Promise<BrowserBackendsResponse> {
  return request<BrowserBackendsResponse>("/api/browser/backends")
}

export async function getBrowserConfig(): Promise<BrowserConfig> {
  return request<BrowserConfig>("/api/browser")
}

export async function saveBrowserConfig(cfg: BrowserConfig): Promise<void> {
  await request<{ status: string }>("/api/browser", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(cfg),
  })
}

export async function installBrowserBackend(
  backend: string,
): Promise<{ message: string }> {
  return request(
    `/api/browser/install?backend=${encodeURIComponent(backend)}`,
    { method: "POST" },
  )
}

export async function uninstallBrowserBackend(
  backend: string,
): Promise<{ message: string }> {
  return request(
    `/api/browser/uninstall?backend=${encodeURIComponent(backend)}`,
    { method: "POST" },
  )
}
