import { IconCheck, IconX } from "@tabler/icons-react"
import dayjs from "dayjs"
import { type ReactNode } from "react"
import { useTranslation } from "react-i18next"

import type { DHTStatus, NetworkStatusResponse } from "@/api/network"
import { Badge } from "@/components/ui/badge"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"

interface DhtPanelProps {
  response?: NetworkStatusResponse
  isLoading: boolean
}

function DhtRow({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-4 py-2">
      <span className="text-muted-foreground text-sm">{label}</span>
      <span className="text-right text-sm">{value}</span>
    </div>
  )
}

function BooleanBadge({ value }: { value: boolean }) {
  return value ? (
    <Badge variant="default" className="shrink-0">
      <IconCheck className="mr-1 size-3" />
      Yes
    </Badge>
  ) : (
    <Badge variant="secondary" className="shrink-0">
      <IconX className="mr-1 size-3" />
      No
    </Badge>
  )
}

export function DhtPanel({ response, isLoading }: DhtPanelProps) {
  const { t } = useTranslation()
  const dht: DHTStatus | undefined = response?.dht

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("pages.network.dht", "DHT")}</CardTitle>
        <CardDescription>
          {t("pages.network.dht_rendezvous", "Rendezvous")}
          {dht?.rendezvous ? `: ${dht.rendezvous}` : ""}
        </CardDescription>
      </CardHeader>
      <CardContent>
        {isLoading ? (
          <div className="space-y-4">
            <Skeleton className="h-8 w-full" />
            <Skeleton className="h-8 w-full" />
            <Skeleton className="h-8 w-full" />
          </div>
        ) : !dht ? (
          <div className="text-muted-foreground py-6 text-center text-sm">
            {t("pages.network.no_dht", "No DHT status available.")}
          </div>
        ) : (
          <div className="divide-border divide-y">
            <DhtRow
              label={t("pages.network.dht_mode", "Mode")}
              value={<Badge variant="outline">{dht.mode}</Badge>}
            />
            <DhtRow
              label={t("pages.network.dht_routing_table", "Routing Table")}
              value={dht.routing_table_size}
            />
            <DhtRow
              label={t("pages.network.dht_bootstrap", "Bootstrap Peers")}
              value={dht.bootstrap_peers}
            />
            <DhtRow
              label={t("pages.network.dht_discovered", "Discovered Peers")}
              value={dht.discovered_peer_count}
            />
            <DhtRow
              label={t("pages.network.dht_last_provide", "Last Provide")}
              value={
                dht.last_provide_time
                  ? dayjs(dht.last_provide_time).format("lll")
                  : "-"
              }
            />
            <DhtRow
              label={t("pages.network.dht_last_discover", "Last Discover")}
              value={
                dht.last_discover_time
                  ? dayjs(dht.last_discover_time).format("lll")
                  : "-"
              }
            />
            <DhtRow
              label={t("pages.network.dht_has_provided", "Has Provided")}
              value={<BooleanBadge value={dht.has_provided} />}
            />
            <DhtRow
              label={t("pages.network.dht_has_discovered", "Has Discovered")}
              value={<BooleanBadge value={dht.has_discovered} />}
            />
          </div>
        )}
      </CardContent>
    </Card>
  )
}
