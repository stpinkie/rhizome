import { launcherFetch } from "@/api/http"

export interface NetworkPeerCapability {
  models?: string[]
  skills?: string[]
  agents?: string[]
}

export interface NetworkPeer {
  peer_id: string
  addrs: string[]
  trusted: boolean
  capability?: NetworkPeerCapability
}

export interface DHTStatus {
  rendezvous: string
  rendezvous_cid: string
  mode: string
  routing_table_size: number
  bootstrap_peers: number
  discovered_peer_count: number
  has_provided: boolean
  has_discovered: boolean
  last_provide_time?: string
  last_discover_time?: string
}

export interface NetworkStatusResponse {
  name: string
  node_index: number
  peer_id: string
  identity: string
  peers?: NetworkPeer[]
  dht?: DHTStatus
}

export interface NetworkStatusOptions {
  bootstraps?: string[]
  timeout?: string
  listen?: string[]
}

function buildQuery(options?: NetworkStatusOptions): string {
  const params = new URLSearchParams()
  if (options?.timeout) {
    params.set("timeout", options.timeout)
  }
  for (const b of options?.bootstraps ?? []) {
    if (b.trim() !== "") {
      params.append("bootstrap", b.trim())
    }
  }
  for (const l of options?.listen ?? []) {
    if (l.trim() !== "") {
      params.append("listen", l.trim())
    }
  }
  const query = params.toString()
  return query ? `?${query}` : ""
}

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await launcherFetch(path, options)
  if (!res.ok) {
    throw new Error(await extractErrorMessage(res))
  }
  return res.json() as Promise<T>
}

export async function getNetworkPeers(
  options?: NetworkStatusOptions,
): Promise<NetworkStatusResponse> {
  return request<NetworkStatusResponse>(
    `/api/network/peers${buildQuery(options)}`,
  )
}

export async function getNetworkDHT(
  options?: NetworkStatusOptions,
): Promise<NetworkStatusResponse> {
  return request<NetworkStatusResponse>(
    `/api/network/dht${buildQuery(options)}`,
  )
}

async function extractErrorMessage(res: Response): Promise<string> {
  try {
    const raw = await res.text()
    if (raw.trim() === "") {
      return `API error: ${res.status} ${res.statusText}`
    }
    try {
      const body = JSON.parse(raw) as {
        error?: string
        errors?: string[]
      }
      if (Array.isArray(body.errors) && body.errors.length > 0) {
        return body.errors.join("; ")
      }
      if (typeof body.error === "string" && body.error.trim() !== "") {
        return body.error
      }
    } catch {
      return raw.trim()
    }
  } catch {
    // ignore invalid body
  }
  return `API error: ${res.status} ${res.statusText}`
}
