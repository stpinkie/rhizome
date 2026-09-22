import { useState } from "react"
import { useTranslation } from "react-i18next"

import type { NetworkPeer, NetworkStatusResponse } from "@/api/network"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { cn } from "@/lib/utils"

import { PeerDetailSheet } from "./peer-detail-sheet"

interface TopologyPanelProps {
  response?: NetworkStatusResponse
  isLoading: boolean
}

// Edge geometry: the local node sits at the center and peers are spread
// on an ellipse. Direction maps to arrow/dash, transport to color, and
// latency to stroke width.
const W = 720
const H = 320
const CX = W / 2
const CY = H / 2
const RX = W / 2 - 110
const RY = H / 2 - 60

function edgeColorClass(transport: string): string {
  switch (transport) {
    case "quic":
      return "text-emerald-500"
    case "tcp":
      return "text-sky-500"
    case "relay":
      return "text-amber-500"
    default:
      return "text-muted-foreground"
  }
}

// primaryConn picks the edge's representative connection — a direct
// transport beats relayed when a peer holds several.
function primaryConn(peer: NetworkPeer) {
  const conns = peer.conns ?? []
  return conns.find((c) => c.transport !== "relay") ?? conns[0]
}

function edgeWidth(latencyMs?: number): number {
  if (!latencyMs || latencyMs <= 0) return 1.5
  return 1.5 + Math.min(latencyMs / 60, 3)
}

function shortID(id: string): string {
  return id.length <= 16 ? id : `${id.slice(0, 8)}…${id.slice(-4)}`
}

export function TopologyPanel({ response, isLoading }: TopologyPanelProps) {
  const { t } = useTranslation()
  const peers = response?.peers ?? []
  const [selected, setSelected] = useState<NetworkPeer | undefined>()

  // Spread peers evenly over the ellipse; -90° puts the first on top.
  const nodes = peers.map((peer, i) => {
    const angle = (2 * Math.PI * i) / peers.length - Math.PI / 2
    return {
      peer,
      x: CX + RX * Math.cos(angle),
      y: CY + RY * Math.sin(angle),
    }
  })

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("pages.network.topology", "Topology")}</CardTitle>
        <CardDescription>
          {t(
            "pages.network.topology_desc",
            "Live connections — color is transport, thickness is latency, arrows show dial direction.",
          )}
        </CardDescription>
      </CardHeader>
      <CardContent>
        {isLoading ? (
          <div className="bg-muted/30 h-64 animate-pulse rounded-lg" />
        ) : peers.length === 0 ? (
          <div className="text-muted-foreground py-10 text-center text-sm">
            {t(
              "pages.network.topology_empty",
              "No connected peers — the graph appears once peers connect.",
            )}
          </div>
        ) : (
          <>
            <svg
              viewBox={`0 0 ${W} ${H}`}
              className="w-full"
              role="img"
              aria-label={t("pages.network.topology", "Topology")}
            >
              <defs>
                <marker
                  id="topo-arrow-out"
                  markerWidth="8"
                  markerHeight="8"
                  refX="7"
                  refY="4"
                  orient="auto"
                >
                  <path d="M0,0 L8,4 L0,8 z" fill="context-stroke" />
                </marker>
                <marker
                  id="topo-arrow-in"
                  markerWidth="8"
                  markerHeight="8"
                  refX="1"
                  refY="4"
                  orient="auto-start-reverse"
                >
                  <path d="M0,0 L8,4 L0,8 z" fill="context-stroke" />
                </marker>
              </defs>

              {nodes.map(({ peer, x, y }) => {
                const conn = primaryConn(peer)
                const transport = conn?.transport ?? "other"
                const inbound = conn?.direction === "inbound"
                const relayed = transport === "relay"
                // Pull the line ends back so arrows don't hide under nodes.
                const dx = x - CX
                const dy = y - CY
                const len = Math.hypot(dx, dy) || 1
                const x1 = CX + (dx / len) * 16
                const y1 = CY + (dy / len) * 16
                const x2 = x - (dx / len) * 22
                const y2 = y - (dy / len) * 22
                return (
                  <line
                    key={`e-${peer.peer_id}`}
                    x1={x1}
                    y1={y1}
                    x2={x2}
                    y2={y2}
                    stroke="currentColor"
                    strokeWidth={edgeWidth(peer.latency_ms)}
                    strokeDasharray={relayed || inbound ? "5 4" : undefined}
                    markerEnd={inbound ? undefined : "url(#topo-arrow-out)"}
                    markerStart={inbound ? "url(#topo-arrow-in)" : undefined}
                    className={cn(edgeColorClass(transport), "opacity-70")}
                  />
                )
              })}

              {/* Self */}
              <g>
                <circle
                  cx={CX}
                  cy={CY}
                  r={12}
                  className="fill-primary stroke-background"
                  strokeWidth={2}
                />
                <text
                  x={CX}
                  y={CY + 28}
                  textAnchor="middle"
                  className="fill-foreground text-[11px] font-medium"
                >
                  {t("pages.network.this_node", "this node")}
                </text>
              </g>

              {nodes.map(({ peer, x, y }) => (
                <g
                  key={peer.peer_id}
                  transform={`translate(${x},${y})`}
                  className="cursor-pointer"
                  onClick={() => setSelected(peer)}
                >
                  <circle
                    r={10}
                    className={cn(
                      "stroke-background hover:r-12 transition-all",
                      peer.trusted ? "fill-sky-500" : "fill-muted-foreground",
                    )}
                    strokeWidth={2}
                  />
                  <text
                    y={-18}
                    textAnchor="middle"
                    className="fill-foreground font-mono text-[10px]"
                  >
                    {shortID(peer.peer_id)}
                  </text>
                  {peer.capability?.role && (
                    <text
                      y={26}
                      textAnchor="middle"
                      className="fill-amber-500 text-[9px] font-medium uppercase"
                    >
                      {peer.capability.role}
                    </text>
                  )}
                  {peer.latency_ms !== undefined && peer.latency_ms > 0 && (
                    <text
                      y={peer.capability?.role ? 38 : 26}
                      textAnchor="middle"
                      className="fill-muted-foreground text-[9px]"
                    >
                      {peer.latency_ms.toFixed(0)} ms
                    </text>
                  )}
                </g>
              ))}
            </svg>

            <div className="text-muted-foreground mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs">
              <span className="text-emerald-500">— quic</span>
              <span className="text-sky-500">— tcp</span>
              <span className="text-amber-500">┅ relay</span>
              <span>
                → {t("pages.network.outbound", "outbound")} · ⇠{" "}
                {t("pages.network.inbound", "inbound")} (dashed)
              </span>
              <span className="ml-auto">
                {t("pages.network.topology_hint", "click a node for details")}
              </span>
            </div>
          </>
        )}
        <PeerDetailSheet
          peer={selected}
          onClose={() => setSelected(undefined)}
        />
      </CardContent>
    </Card>
  )
}
