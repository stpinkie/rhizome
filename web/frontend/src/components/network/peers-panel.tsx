import { IconCheck, IconWorld, IconX } from "@tabler/icons-react"
import { useTranslation } from "react-i18next"

import type { NetworkStatusResponse } from "@/api/network"
import { Badge } from "@/components/ui/badge"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"

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

export function PeersPanel({ response, isLoading }: PeersPanelProps) {
  const { t } = useTranslation()
  const peers = response?.peers ?? []

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
            {peers.map((peer) => (
              <div key={peer.peer_id} className="bg-muted/40 rounded-lg p-4">
                <div className="flex items-start justify-between gap-3">
                  <div className="flex items-center gap-2">
                    <IconWorld className="text-muted-foreground size-4" />
                    <span className="font-mono text-sm break-all">
                      {peer.peer_id}
                    </span>
                  </div>
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

                {peer.addrs.length > 0 && (
                  <div className="mt-2 space-y-1">
                    <span className="text-muted-foreground text-xs">
                      {t("pages.network.addrs", "Addresses")}
                    </span>
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
              </div>
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  )
}
