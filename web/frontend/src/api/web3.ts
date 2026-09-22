import { launcherFetch } from "@/api/http"

export interface WalletAddress {
  address: string
  label?: string
  created_at?: string
  default?: boolean
  balance_wei?: string
}

export interface PendingEntry {
  id: string
  kind: "send" | "sign" | "approve" | "contract"
  chain_id: number
  from: string
  to?: string
  value_wei?: string
  data?: string
  selector?: string
  message?: string
  summary: string
  created_at: string
  expires_at: string
  status:
    | "pending"
    | "approved"
    | "sent"
    | "done"
    | "rejected"
    | "expired"
    | "failed"
  resolved_by?: string
  tx_hash?: string
  result?: string
  error?: string
}

export interface PendingListResponse {
  pending: PendingEntry[]
  count: number
}

export interface WalletResponse {
  addresses: WalletAddress[]
  default?: string
  count: number
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

export async function getWeb3Pending(): Promise<PendingListResponse> {
  return request<PendingListResponse>("/api/web3/pending")
}

export async function getWeb3Wallet(): Promise<WalletResponse> {
  return request<WalletResponse>("/api/web3/wallet?balances=true")
}

export async function resolveWeb3Approval(
  id: string,
  action: "approve" | "reject",
): Promise<PendingEntry> {
  return request<PendingEntry>(
    `/api/web3/approvals/${encodeURIComponent(id)}`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ action }),
    },
  )
}
