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
  reachability?: string
  addrs?: string[]
  relayed_addrs?: string[]
  peers?: NetworkPeer[]
  dht?: DHTStatus
}

export interface SavedPeerCapability {
  models?: string[]
  skills?: string[]
  agents?: string[]
}

export interface SavedPeer {
  peer_id: string
  bootstrap_addrs: string[]
  trusted: boolean
  connected?: boolean
  capability?: SavedPeerCapability
}

export interface SavedPeersResponse {
  peer_id: string
  saved_peers: SavedPeer[]
}

export interface NetworkStatusOptions {
  bootstraps?: string[]
  timeout?: string
  listen?: string[]
  trust?: boolean
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
  if (options?.trust) {
    params.set("trust", "true")
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

export async function getNetworkStatus(
  options?: NetworkStatusOptions,
): Promise<NetworkStatusResponse> {
  return request<NetworkStatusResponse>(
    `/api/network/status${buildQuery(options)}`,
  )
}

export async function getNetworkSavedPeers(): Promise<SavedPeersResponse> {
  return request<SavedPeersResponse>(`/api/network/saved-peers`)
}

export async function untrustNetworkPeer(peerID: string): Promise<SavedPeer> {
  const params = new URLSearchParams({ action: "untrust", peer: peerID })
  return request<SavedPeer>(`/api/network/saved-peers?${params.toString()}`, {
    method: "POST",
  })
}

export async function removeNetworkPeer(peerID: string): Promise<void> {
  const params = new URLSearchParams({ peer: peerID })
  await request<void>(`/api/network/saved-peers?${params.toString()}`, {
    method: "DELETE",
  })
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
