import { IconCheck, IconWorld, IconX } from "@tabler/icons-react"
import { useState } from "react"
import { useTranslation } from "react-i18next"

import type { NetworkPeer, NetworkStatusResponse } from "@/api/network"
import { Badge } from "@/components/ui/badge"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"

interface PeersPanelProps {
  response?: NetworkStatusResponse
  isLoading: boolean
}

function PeerPill({ label, items }: { label: string; items?: string[] }) {
  if (!items || items.length === 0) return null
  return (
    <div className="mt-2">
      <span className="text-muted-foreground text-xs">{label}</span>
      <div className="mt-1 flex flex-wrap gap-1.5">
        {items.map((item) => (
          <Badge key={item} variant="secondary" className="text-xs">
            {item}
          </Badge>
        ))}
      </div>
    </div>
  )
}

function transportVariant(
  transport: string,
): "default" | "secondary" | "outline" {
  switch (transport) {
    case "quic":
      return "default"
    case "relay":
      return "secondary"
    default:
      return "outline"
  }
}

function peerTransports(peer: NetworkPeer): string[] {
  const seen = new Set<string>()
  for (const c of peer.conns ?? []) {
    if (c.transport) seen.add(c.transport)
  }
  return [...seen]
}

function formatBytes(n: number): string {
  if (n >= 1 << 30) return `${(n / (1 << 30)).toFixed(1)} GiB`
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MiB`
  if (n >= 1 << 10) return `${(n / (1 << 10)).toFixed(1)} KiB`
  return `${n} B`
}

function ScoreBadge({ peer }: { peer: NetworkPeer }) {
  const { t } = useTranslation()
  const score = peer.score
  if (!score) return null
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Badge variant="outline" className="shrink-0 cursor-default">
          {t("pages.network.peer_score", "score")} {score.score.toFixed(0)}
        </Badge>
      </TooltipTrigger>
      <TooltipContent>
        <div className="space-y-0.5 text-xs">
          <div>
            {score.successes} {t("pages.network.score_ok", "ok")} /{" "}
            {score.failures} {t("pages.network.score_failed", "failed")}
          </div>
          <div>
            {t("pages.network.score_avg", "avg")}{" "}
            {score.avg_latency_ms.toFixed(0)} ms
          </div>
          {score.last_error && (
            <div className="text-destructive">{score.last_error}</div>
          )}
        </div>
      </TooltipContent>
    </Tooltip>
  )
}

function PeerDetailRow({
  label,
  value,
}: {
  label: string
  value?: string | number
}) {
  if (value === undefined || value === "") return null
  return (
    <div className="flex items-start justify-between gap-4 py-1">
      <span className="text-muted-foreground text-xs">{label}</span>
      <span className="text-right font-mono text-xs break-all">{value}</span>
    </div>
  )
}

function PeerDetailSheet({
  peer,
  onClose,
}: {
  peer?: NetworkPeer
  onClose: () => void
}) {
  const { t } = useTranslation()
  return (
    <Sheet open={!!peer} onOpenChange={(open) => !open && onClose()}>
      <SheetContent className="overflow-y-auto">
        <SheetHeader>
          <SheetTitle className="font-mono text-sm break-all">
            {peer?.peer_id}
          </SheetTitle>
          <SheetDescription>
            {peer?.trusted
              ? t("pages.network.trusted", "Trusted")
              : t("pages.network.untrusted", "Untrusted")}
          </SheetDescription>
        </SheetHeader>
        {peer && (
          <div className="space-y-4 px-4 pb-4">
            <div>
              <h4 className="text-muted-foreground mb-1 text-xs font-medium uppercase">
                {t("pages.network.peer_quality", "Quality")}
              </h4>
              <PeerDetailRow
                label={t("pages.network.latency", "Latency")}
                value={
                  peer.latency_ms
                    ? `${peer.latency_ms.toFixed(1)} ms`
                    : undefined
                }
              />
              <PeerDetailRow
                label={t("pages.network.peer_score", "Score")}
                value={
                  peer.score
                    ? `${peer.score.score.toFixed(0)} (${peer.score.successes} ok / ${peer.score.failures} failed)`
                    : undefined
                }
              />
              <PeerDetailRow
                label={t("pages.network.last_seen", "Last seen")}
                value={peer.last_seen}
              />
              {peer.bandwidth && (
                <PeerDetailRow
                  label={t("pages.network.bandwidth", "Bandwidth")}
                  value={`↓${formatBytes(peer.bandwidth.total_in)} (${formatBytes(peer.bandwidth.rate_in)}/s) ↑${formatBytes(peer.bandwidth.total_out)} (${formatBytes(peer.bandwidth.rate_out)}/s)`}
                />
              )}
            </div>

            {peer.conns && peer.conns.length > 0 && (
              <div>
                <h4 className="text-muted-foreground mb-1 text-xs font-medium uppercase">
                  {t("pages.network.connections", "Connections")}
                </h4>
                <ul className="space-y-2">
                  {peer.conns.map((c, i) => (
                    <li key={i} className="bg-muted/40 rounded-md p-2 text-xs">
                      <div className="flex items-center gap-2">
                        <Badge variant={transportVariant(c.transport)}>
                          {c.transport || "other"}
                        </Badge>
                        <span className="text-muted-foreground">
                          {c.direction}
                        </span>
                        <span className="text-muted-foreground ml-auto">
                          {c.stream_count}{" "}
                          {t("pages.network.streams", "streams")}
                        </span>
                      </div>
                      <div className="text-muted-foreground mt-1 font-mono break-all">
                        {c.remote_multiaddr}
                      </div>
                      {c.opened_at && (
                        <div className="text-muted-foreground mt-0.5">
                          {t("pages.network.opened_at", "since")}{" "}
                          {new Date(c.opened_at).toLocaleString()}
                        </div>
                      )}
                    </li>
                  ))}
                </ul>
              </div>
            )}

            {peer.addrs.length > 0 && (
              <div>
                <h4 className="text-muted-foreground mb-1 text-xs font-medium uppercase">
                  {t("pages.network.addrs", "Addresses")}
                </h4>
                <ul className="space-y-0.5">
                  {peer.addrs.map((addr) => (
                    <li
                      key={addr}
                      className="text-muted-foreground font-mono text-xs break-all"
                    >
                      {addr}
                    </li>
                  ))}
                </ul>
              </div>
            )}

            <PeerPill
              label={t("pages.network.models", "Models")}
              items={peer.capability?.models}
            />
            <PeerPill
              label={t("pages.network.skills", "Skills")}
              items={peer.capability?.skills}
            />
            <PeerPill
              label={t("pages.network.agents", "Agents")}
              items={peer.capability?.agents}
            />

            {peer.capability?.agent_manifests &&
              Object.keys(peer.capability.agent_manifests).length > 0 && (
                <div>
                  <h4 className="text-muted-foreground mb-1 text-xs font-medium uppercase">
                    {t("pages.network.manifests", "Manifest fingerprints")}
                  </h4>
                  <ul className="space-y-0.5">
                    {Object.entries(peer.capability.agent_manifests).map(
                      ([id, fp]) => (
                        <li key={id} className="font-mono text-xs break-all">
                          <span className="text-foreground">{id}</span>{" "}
                          <span className="text-muted-foreground">{fp}</span>
                        </li>
                      ),
                    )}
                  </ul>
                </div>
              )}
          </div>
        )}
      </SheetContent>
    </Sheet>
  )
}

export function PeersPanel({ response, isLoading }: PeersPanelProps) {
  const { t } = useTranslation()
  const peers = response?.peers ?? []
  const [selected, setSelected] = useState<NetworkPeer | undefined>()

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("pages.network.peers", "Peers")}</CardTitle>
        <CardDescription>
          {response?.peer_id
            ? `${t("pages.network.peer_id", "Peer ID")}: ${response.peer_id}`
            : t("pages.network.peers", "Connected peers")}
        </CardDescription>
      </CardHeader>
      <CardContent>
        {isLoading ? (
          <div className="space-y-4">
            <Skeleton className="h-20 w-full" />
            <Skeleton className="h-20 w-full" />
          </div>
        ) : peers.length === 0 ? (
          <div className="text-muted-foreground py-6 text-center text-sm">
            <p>{t("pages.network.no_peers", "No connected peers found.")}</p>
            <p className="mt-1 opacity-70">
              {t(
                "pages.network.no_peers_hint",
                "Add a bootstrap peer or check DHT/mDNS configuration.",
              )}
            </p>
          </div>
        ) : (
          <div className="space-y-4">
            {peers.map((peer) => {
              const transports = peerTransports(peer)
              return (
                <div
                  key={peer.peer_id}
                  className="bg-muted/40 hover:bg-muted/60 cursor-pointer rounded-lg p-4 transition-colors"
                  onClick={() => setSelected(peer)}
                >
                  <div className="flex items-start justify-between gap-3">
                    <div className="flex items-center gap-2">
                      <IconWorld className="text-muted-foreground size-4" />
                      <span className="font-mono text-sm break-all">
                        {peer.peer_id}
                      </span>
                    </div>
                    <div className="flex shrink-0 items-center gap-1.5">
                      {transports.map((tr) => (
                        <Badge key={tr} variant={transportVariant(tr)}>
                          {tr}
                        </Badge>
                      ))}
                      <ScoreBadge peer={peer} />
                      <Badge
                        variant={peer.trusted ? "default" : "secondary"}
                        className="shrink-0"
                      >
                        {peer.trusted ? (
                          <>
                            <IconCheck className="mr-1 size-3" />
                            {t("pages.network.trusted", "Trusted")}
                          </>
                        ) : (
                          <>
                            <IconX className="mr-1 size-3" />
                            {t("pages.network.untrusted", "Untrusted")}
                          </>
                        )}
                      </Badge>
                    </div>
                  </div>

                  {peer.latency_ms !== undefined && peer.latency_ms > 0 && (
                    <div className="text-muted-foreground mt-1 text-xs">
                      {peer.latency_ms.toFixed(0)} ms
                    </div>
                  )}

                  <PeerPill
                    label={t("pages.network.models", "Models")}
                    items={peer.capability?.models}
                  />
                  <PeerPill
                    label={t("pages.network.skills", "Skills")}
                    items={peer.capability?.skills}
                  />
                  <PeerPill
                    label={t("pages.network.agents", "Agents")}
                    items={peer.capability?.agents}
                  />
                </div>
              )
            })}
          </div>
        )}
        <PeerDetailSheet
          peer={selected}
          onClose={() => setSelected(undefined)}
        />
      </CardContent>
    </Card>
  )
}
