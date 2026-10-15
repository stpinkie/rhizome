import {
  IconAlertTriangle,
  IconCoins,
  IconLoader2,
  IconRefresh,
} from "@tabler/icons-react"
import { useTranslation } from "react-i18next"

import type { EconEntry, EconInflight, PeerEconSummary } from "@/api/network"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { ScrollArea } from "@/components/ui/scroll-area"
import { useEconomy } from "@/hooks/use-economy"

function truncID(id: string): string {
  if (!id) return "—"
  return id.length > 18 ? `${id.slice(0, 10)}…${id.slice(-6)}` : id
}

function stateVariant(
  state: string,
): "default" | "secondary" | "outline" | "destructive" {
  switch (state) {
    case "settled":
      return "default"
    case "disputed":
    case "rejected":
    case "failed":
    case "stalled":
      return "destructive"
    case "accrued":
      return "secondary"
    default:
      return "outline"
  }
}

function PeerEconRow({ peerID, s }: { peerID: string; s: PeerEconSummary }) {
  const { t } = useTranslation()
  return (
    <div className="bg-muted/40 rounded-lg px-3 py-2 text-xs">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-mono" title={peerID}>
          {truncID(peerID)}
        </span>
        {s.overdue && s.overdue.length > 0 && (
          <Badge variant="destructive">
            <IconAlertTriangle className="mr-1 size-3" />
            {t("pages.network.econ_overdue", "overdue")}
          </Badge>
        )}
        {(!s.overdue || s.overdue.length === 0) &&
          s.over_threshold &&
          s.over_threshold.length > 0 && (
            <Badge variant="secondary">
              {t("pages.network.econ_settle_due", "settle due")}
            </Badge>
          )}
        <span className="text-muted-foreground ml-auto">
          {t("pages.network.econ_billed", "billed")} {s.charges_issued} ·{" "}
          {t("pages.network.econ_charged", "charged")} {s.charges_received} ·{" "}
          {t("pages.network.econ_settled_ct", "settled")} {s.settled}
          {s.disputed > 0 &&
            ` · ${t("pages.network.econ_disputed_ct", "disputed")} ${s.disputed}`}
          {s.written_off
            ? ` · ${t("pages.network.econ_written_off", "written off")} ${s.written_off}`
            : ""}
        </span>
      </div>
      {s.balances && s.balances.length > 0 && (
        <div className="text-muted-foreground mt-1 flex flex-wrap gap-x-4 gap-y-0.5 font-mono">
          {s.balances.map((b) => (
            <span key={b.unit}>
              {b.unit}: {t("pages.network.econ_owe", "owe")} {b.payable_accrued}{" "}
              · {t("pages.network.econ_owed", "owed")} {b.receivable_accrued}
            </span>
          ))}
        </div>
      )}
    </div>
  )
}

function InflightRow({ r }: { r: EconInflight }) {
  return (
    <div className="bg-muted/40 flex flex-wrap items-center gap-2 rounded-lg px-3 py-2 text-xs">
      <Badge variant={stateVariant(r.state)}>{r.state}</Badge>
      <span className="font-mono" title={r.peer_id}>
        {truncID(r.peer_id)}
      </span>
      <span className="text-muted-foreground">{r.unit}</span>
      {r.tx_hash && (
        <span className="text-muted-foreground font-mono" title={r.tx_hash}>
          tx {truncID(r.tx_hash)}
        </span>
      )}
      {r.error && <span className="text-destructive">{r.error}</span>}
      <span className="text-muted-foreground ml-auto">
        {new Date(r.updated_at).toLocaleString()}
      </span>
    </div>
  )
}

function ChargeRow({ e }: { e: EconEntry }) {
  return (
    <tr className="border-b last:border-0">
      <td className="py-1.5 pr-3">
        <Badge variant={stateVariant(e.state)}>{e.state}</Badge>
      </td>
      <td className="py-1.5 pr-3 font-mono text-xs" title={e.peer_id}>
        {truncID(e.peer_id)}
      </td>
      <td className="text-muted-foreground py-1.5 pr-3 text-xs">
        {e.direction}
      </td>
      <td className="py-1.5 pr-3 text-xs whitespace-nowrap">
        {e.amount} {e.unit}
      </td>
      <td
        className="text-muted-foreground py-1.5 pr-3 font-mono text-xs"
        title={e.task_id ?? e.correlation_id}
      >
        {truncID(e.task_id ?? e.correlation_id ?? "")}
      </td>
      <td className="text-muted-foreground py-1.5 text-xs whitespace-nowrap">
        {new Date(e.ts).toLocaleDateString()}
      </td>
    </tr>
  )
}

/**
 * Paired-settlement economy (v0.17.0 Track 146): per-peer charge splits,
 * accrued balances with settle-threshold flags, in-flight web3 close-outs,
 * and the recent charge tail — proxied from the daemon (or the launcher's
 * daemonless ledger read) via /api/network/economy.
 */
export function EconomyPanel() {
  const { t } = useTranslation()
  const query = useEconomy()
  const data = query.data
  const peers = Object.entries(data?.peers ?? {}).sort(([a], [b]) =>
    a.localeCompare(b),
  )
  const charges = [...(data?.recent_charges ?? [])].reverse()

  return (
    <Card>
      <CardHeader>
        <div className="flex items-start justify-between gap-2">
          <div>
            <CardTitle className="flex items-center gap-2">
              <IconCoins className="size-4" />
              {t("pages.network.economy", "Economy")}
            </CardTitle>
            <CardDescription>
              {t(
                "pages.network.economy_description",
                "Paired-settlement ledger: per-peer balances, settle due / overdue flags, and in-flight close-outs.",
              )}
            </CardDescription>
          </div>
          <Button
            variant="outline"
            size="sm"
            onClick={() => void query.refetch()}
            disabled={query.isFetching}
          >
            <IconRefresh
              className={`size-4 ${query.isFetching ? "animate-spin" : ""}`}
            />
          </Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-4">
        {query.isLoading ? (
          <p className="text-muted-foreground flex items-center gap-2 text-sm">
            <IconLoader2 className="size-4 animate-spin" />
            {t("pages.network.economy_loading", "Loading economy state…")}
          </p>
        ) : query.error ? (
          <p className="text-destructive text-sm">
            {query.error instanceof Error
              ? query.error.message
              : String(query.error)}
          </p>
        ) : !data?.enabled ? (
          <p className="text-muted-foreground text-sm">
            {t(
              "pages.network.economy_disabled",
              "Paired settlement is off — set mesh.economy.enabled to start billing and settling with peers.",
            )}
          </p>
        ) : (
          <>
            <div className="text-muted-foreground flex flex-wrap gap-x-4 gap-y-1 text-xs">
              {data.unit && (
                <span>
                  {t("pages.network.econ_unit", "unit")}: {data.unit}
                </span>
              )}
              {data.settle_threshold && (
                <span>
                  {t("pages.network.econ_threshold", "settle threshold")}:{" "}
                  {data.settle_threshold}
                </span>
              )}
              {data.settle_grace_period && (
                <span>
                  {t("pages.network.econ_grace", "grace")}:{" "}
                  {data.settle_grace_period}
                </span>
              )}
            </div>

            <div>
              <div className="mb-1.5 text-sm font-medium">
                {t("pages.network.econ_peers", "Peer balances")}
              </div>
              {peers.length === 0 ? (
                <p className="text-muted-foreground text-sm">
                  {t(
                    "pages.network.econ_peers_empty",
                    "No ledger entries yet — billed remote work lands here.",
                  )}
                </p>
              ) : (
                <div className="space-y-1.5">
                  {peers.map(([pid, s]) => (
                    <PeerEconRow key={pid} peerID={pid} s={s} />
                  ))}
                </div>
              )}
            </div>

            {data.inflight && data.inflight.length > 0 && (
              <div>
                <div className="mb-1.5 text-sm font-medium">
                  {t("pages.network.econ_inflight", "In-flight settlements")}
                </div>
                <div className="space-y-1.5">
                  {data.inflight.map((r) => (
                    <InflightRow key={`${r.peer_id}|${r.unit}`} r={r} />
                  ))}
                </div>
              </div>
            )}

            <div>
              <div className="mb-1.5 text-sm font-medium">
                {t("pages.network.econ_recent", "Recent charges")}
              </div>
              {charges.length === 0 ? (
                <p className="text-muted-foreground text-sm">
                  {t(
                    "pages.network.econ_recent_empty",
                    "No charges yet — the ledger tail lands here.",
                  )}
                </p>
              ) : (
                <ScrollArea className="max-h-64">
                  <table className="w-full text-xs">
                    <tbody>
                      {charges.map((e) => (
                        <ChargeRow key={e.entry_id + e.ts} e={e} />
                      ))}
                    </tbody>
                  </table>
                </ScrollArea>
              )}
            </div>
          </>
        )}
      </CardContent>
    </Card>
  )
}
