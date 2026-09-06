import { IconAntenna, IconWorld } from "@tabler/icons-react"
import { type ReactNode } from "react"
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

interface NodePanelProps {
  response?: NetworkStatusResponse
  isLoading: boolean
}

function NodeRow({ label, value }: { label: ReactNode; value: ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-4 py-2">
      <span className="text-muted-foreground text-sm">{label}</span>
      <span className="text-right text-sm break-all">{value}</span>
    </div>
  )
}

function AddrList({ addrs }: { addrs: string[] }) {
  return (
    <ul className="space-y-0.5">
      {addrs.map((addr) => (
        <li
          key={addr}
          className="text-muted-foreground font-mono text-xs break-all"
        >
          {addr}
        </li>
      ))}
    </ul>
  )
}

export function NodePanel({ response, isLoading }: NodePanelProps) {
  const { t } = useTranslation()
  const reachability = response?.reachability
  const addrs = response?.addrs ?? []
  const relayed = response?.relayed_addrs ?? []

  const reachabilityBadge = () => {
    switch (reachability) {
      case "Public":
        return (
          <Badge variant="default">
            {t("pages.network.reachability_public", "Public")}
          </Badge>
        )
      case "Private":
        return (
          <Badge variant="secondary">
            {t("pages.network.reachability_private", "Private (NAT'd)")}
          </Badge>
        )
      default:
        return (
          <Badge variant="outline">
            {t("pages.network.reachability_unknown", "Unknown")}
          </Badge>
        )
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <IconWorld className="size-4" />
          {t("pages.network.this_node", "This Node")}
        </CardTitle>
        <CardDescription>
          {response?.peer_id
            ? `${t("pages.network.peer_id", "Peer ID")}: ${response.peer_id}`
            : t("pages.network.this_node_desc", "Local node network status")}
        </CardDescription>
      </CardHeader>
      <CardContent>
        {isLoading ? (
          <div className="space-y-4">
            <Skeleton className="h-8 w-full" />
            <Skeleton className="h-8 w-full" />
            <Skeleton className="h-8 w-full" />
          </div>
        ) : !response ? (
          <div className="text-muted-foreground py-6 text-center text-sm">
            {t("pages.network.no_status", "No node status available.")}
          </div>
        ) : (
          <div className="divide-border divide-y">
            <NodeRow
              label={t("pages.network.reachability", "Reachability")}
              value={reachabilityBadge()}
            />
            {relayed.length > 0 && (
              <NodeRow
                label={
                  <span className="inline-flex items-center gap-1">
                    <IconAntenna className="size-4" />
                    {t("pages.network.relayed_addrs", "Relayed Addresses")}
                  </span>
                }
                value={<AddrList addrs={relayed} />}
              />
            )}
            <NodeRow
              label={t("pages.network.listen_addrs", "Advertised Addresses")}
              value={addrs.length > 0 ? <AddrList addrs={addrs} /> : "-"}
            />
          </div>
        )}
      </CardContent>
    </Card>
  )
}
