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
import { Skeleton } from "@/components/ui/skeleton"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"

import { transportVariant } from "./format"
import { PeerDetailSheet, PeerPill } from "./peer-detail-sheet"

interface PeersPanelProps {
  response?: NetworkStatusResponse
  isLoading: boolean
}

function peerTransports(peer: NetworkPeer): string[] {
  const seen = new Set<string>()
  for (const c of peer.conns ?? []) {
    if (c.transport) seen.add(c.transport)
  }
  return [...seen]
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
                      {peer.capability?.role && (
                        <Badge variant="secondary">
                          {peer.capability.role}
                        </Badge>
                      )}
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
