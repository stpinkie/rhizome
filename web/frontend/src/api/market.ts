import { launcherFetch } from "@/api/http"

/**
 * Market dashboard types — mirrors the rhizome-market module's
 * /v1/sessions payload (Track 135): buy-side purchase ledger, live
 * sell-side sessions, dispute-state purchases, and spend reporting.
 * The launcher proxies the module's loopback API verbatim.
 */

export interface MarketSessionRow {
  purchase_id: string
  provider: string
  peer_id?: string
  offer_id: string
  state: string
  price: string
  asset: string
  session_id?: string
  settlement: string
  drawdown?: boolean
  result_sha256?: string
  redundant_group?: string
  tee_attestation?: {
    kind: string
    report_url?: string
    evidence_hash?: string
  }
  error?: string
  created_at: string
  updated_at: string
}

export interface MarketSellSession {
  session_id: string
  escrow_id: string
  peer: string
  offer_id: string
  state: string
  amount?: string
  token?: string
  drawdown?: boolean
  terms_hash?: string
  opened_at: string
  duration_ms: number
}

export interface MarketSpendAsset {
  spent_24h: string
  headroom?: string
}

export interface MarketSpend {
  cap_per_task?: string
  cap_per_day?: string
  assets?: Record<string, MarketSpendAsset>
}

export interface MarketSessionsResponse {
  /** False when the rhizome-market module isn't installed at all. */
  installed: boolean
  sessions: MarketSessionRow[]
  sell_sessions: MarketSellSession[]
  disputes: MarketSessionRow[]
  spend?: MarketSpend
}

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await launcherFetch(path, options)
  if (!res.ok) {
    let detail = `${res.status} ${res.statusText}`
    try {
      const body = await res.text()
      const parsed = JSON.parse(body)
      detail = parsed.error ?? parsed.detail ?? body.trim() ?? detail
    } catch {
      // keep default
    }
    throw new Error(detail)
  }
  return res.json() as Promise<T>
}

export async function getMarketSessions(): Promise<MarketSessionsResponse> {
  return request<MarketSessionsResponse>("/api/market/sessions")
}
