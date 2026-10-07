import {
  IconGavel,
  IconLoader2,
  IconRefresh,
  IconShoppingCart,
} from "@tabler/icons-react"
import { useTranslation } from "react-i18next"

import type { MarketSellSession, MarketSessionRow } from "@/api/market"
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
import { useMarketSessions } from "@/hooks/use-market"

function stateVariant(
  state: string,
): "default" | "secondary" | "outline" | "destructive" {
  switch (state) {
    case "completed":
    case "active":
      return "default"
    case "disputed":
    case "disputable":
    case "failed":
      return "destructive"
    case "resolved":
    case "refunded":
      return "secondary"
    default:
      return "outline"
  }
}

function truncID(id: string): string {
  if (!id) return "—"
  return id.length > 18 ? `${id.slice(0, 10)}…${id.slice(-6)}` : id
}

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`
  if (ms < 60_000) return `${Math.round(ms / 1000)}s`
  return `${Math.floor(ms / 60000)}m${Math.round((ms % 60000) / 1000)}s`
}

function BuyRow({ row }: { row: MarketSessionRow }) {
  const { t } = useTranslation()
  return (
    <tr className="border-b last:border-0">
      <td className="py-1.5 pr-3">
        <Badge variant={stateVariant(row.state)}>{row.state}</Badge>
        {row.drawdown ? (
          <Badge variant="outline" className="ml-1">
            {t("pages.network.market_drawdown", "drawdown")}
          </Badge>
        ) : null}
      </td>
      <td className="py-1.5 pr-3 font-mono text-xs">{row.provider}</td>
      <td className="text-muted-foreground py-1.5 pr-3 text-xs">
        {row.offer_id}
      </td>
      <td className="py-1.5 pr-3 text-xs whitespace-nowrap">
        {row.price} {row.asset}
      </td>
      <td
        className="text-muted-foreground py-1.5 pr-3 font-mono text-xs"
        title={row.session_id}
      >
        {truncID(row.session_id ?? "")}
      </td>
      <td
        className="text-muted-foreground py-1.5 font-mono text-xs"
        title={row.settlement}
      >
        {row.settlement}
      </td>
    </tr>
  )
}

function SellRow({ row }: { row: MarketSellSession }) {
  const { t } = useTranslation()
  return (
    <tr className="border-b last:border-0">
      <td className="py-1.5 pr-3">
        <Badge variant={stateVariant(row.state)}>{row.state}</Badge>
        {row.drawdown ? (
          <Badge variant="outline" className="ml-1">
            {t("pages.network.market_drawdown", "drawdown")}
          </Badge>
        ) : null}
      </td>
      <td className="py-1.5 pr-3 font-mono text-xs" title={row.peer}>
        {truncID(row.peer)}
      </td>
      <td className="text-muted-foreground py-1.5 pr-3 text-xs">
        {row.offer_id}
      </td>
      <td className="text-muted-foreground py-1.5 pr-3 font-mono text-xs">
        {formatDuration(row.duration_ms)}
      </td>
      <td
        className="text-muted-foreground py-1.5 font-mono text-xs"
        title={row.escrow_id}
      >
        {truncID(row.escrow_id)}
      </td>
    </tr>
  )
}

/**
 * Market sessions + disputes (v0.16.0 Track 135): buy-side purchase
 * ledger, live sell-side sessions, and dispute-state purchases proxied
 * from the rhizome-market module via /api/market/sessions. The module
 * absent case renders an explicit not-installed posture.
 */
export function MarketPanel() {
  const { t } = useTranslation()
  const query = useMarketSessions()
  const data = query.data

  return (
    <Card>
      <CardHeader>
        <div className="flex items-start justify-between gap-2">
          <div>
            <CardTitle className="flex items-center gap-2">
              <IconShoppingCart className="size-4" />
              {t("pages.network.market", "Market")}
            </CardTitle>
            <CardDescription>
              {t(
                "pages.network.market_description",
                "Escrowed buy sessions, live sell sessions, and dispute state from the rhizome-market module.",
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
            {t("pages.network.market_loading", "Loading market state…")}
          </p>
        ) : query.error ? (
          <p className="text-destructive text-sm">
            {query.error instanceof Error
              ? query.error.message
              : String(query.error)}
          </p>
        ) : !data?.installed ? (
          <p className="text-muted-foreground text-sm">
            {t(
              "pages.network.market_not_installed",
              "rhizome-market module not installed — install it to buy or serve compute.",
            )}
          </p>
        ) : (
          <>
            {data.disputes.length > 0 && (
              <div>
                <div className="mb-1.5 flex items-center gap-2 text-sm font-medium">
                  <IconGavel className="size-4" />
                  {t("pages.network.market_disputes", "Disputes")}
                </div>
                <div className="space-y-1.5">
                  {data.disputes.map((d) => (
                    <div
                      key={d.purchase_id}
                      className="bg-muted/40 flex flex-wrap items-center gap-2 rounded-lg px-3 py-2 text-xs"
                    >
                      <Badge variant={stateVariant(d.state)}>{d.state}</Badge>
                      <span className="font-mono">{d.provider}</span>
                      <span className="text-muted-foreground">
                        {d.offer_id}
                      </span>
                      <span className="text-muted-foreground whitespace-nowrap">
                        {d.price} {d.asset}
                      </span>
                      <span
                        className="text-muted-foreground ml-auto font-mono"
                        title={d.session_id}
                      >
                        {truncID(d.session_id ?? d.purchase_id)}
                      </span>
                    </div>
                  ))}
                </div>
              </div>
            )}

            <div>
              <div className="mb-1.5 text-sm font-medium">
                {t("pages.network.market_buy_sessions", "Buy sessions")}
              </div>
              {data.sessions.length === 0 ? (
                <p className="text-muted-foreground text-sm">
                  {t(
                    "pages.network.market_buy_empty",
                    "No purchases yet — `rhizome market buy` lands here.",
                  )}
                </p>
              ) : (
                <ScrollArea className="max-h-64">
                  <table className="w-full text-xs">
                    <tbody>
                      {data.sessions.map((row) => (
                        <BuyRow key={row.purchase_id} row={row} />
                      ))}
                    </tbody>
                  </table>
                </ScrollArea>
              )}
            </div>

            <div>
              <div className="mb-1.5 text-sm font-medium">
                {t("pages.network.market_sell_sessions", "Sell sessions")}
              </div>
              {data.sell_sessions.length === 0 ? (
                <p className="text-muted-foreground text-sm">
                  {t(
                    "pages.network.market_sell_empty",
                    "No live sell-side sessions — gated opens land here while they run.",
                  )}
                </p>
              ) : (
                <ScrollArea className="max-h-64">
                  <table className="w-full text-xs">
                    <tbody>
                      {data.sell_sessions.map((row) => (
                        <SellRow key={row.session_id} row={row} />
                      ))}
                    </tbody>
                  </table>
                </ScrollArea>
              )}
            </div>

            {data.spend?.assets &&
              Object.keys(data.spend.assets).length > 0 && (
                <div className="text-muted-foreground flex flex-wrap gap-x-4 gap-y-1 text-xs">
                  {Object.entries(data.spend.assets).map(([asset, a]) => (
                    <span key={asset}>
                      {asset}: {t("pages.network.market_spent", "spent")}{" "}
                      {a.spent_24h}
                      {a.headroom !== undefined
                        ? ` · ${t("pages.network.market_headroom", "headroom")} ${a.headroom}`
                        : ""}
                    </span>
                  ))}
                </div>
              )}
          </>
        )}
      </CardContent>
    </Card>
  )
}
