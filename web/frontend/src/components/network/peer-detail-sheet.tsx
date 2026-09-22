import { useTranslation } from "react-i18next"

import type { NetworkPeer } from "@/api/network"
import { Badge } from "@/components/ui/badge"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"

import { formatBytes, transportVariant } from "./format"

export function PeerPill({
  label,
  items,
}: {
  label: string
  items?: string[]
}) {
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

export function PeerDetailSheet({
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
            {peer?.capability?.role && (
              <Badge variant="secondary" className="ml-2">
                {peer.capability.role}
              </Badge>
            )}
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
