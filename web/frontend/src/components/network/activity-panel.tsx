import { IconActivity, IconLoader2 } from "@tabler/icons-react"
import { useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"

import {
  type MeshActivityEntry,
  getNetworkActivity,
  openNetworkEventStream,
} from "@/api/network"
import { Badge } from "@/components/ui/badge"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { ScrollArea } from "@/components/ui/scroll-area"

const MAX_ENTRIES = 100
const POLL_INTERVAL_MS = 10000

function entryKey(e: MeshActivityEntry): string {
  return `${e.time}|${e.kind}|${e.source ?? ""}`
}

function kindVariant(kind: string): "default" | "secondary" | "outline" {
  if (kind.startsWith("swarm.")) return "default"
  if (kind.startsWith("mesh.")) return "secondary"
  return "outline"
}

function formatTime(ts: string): string {
  if (!ts) return ""
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return ts
  return d.toLocaleTimeString()
}

function peerAttr(e: MeshActivityEntry): string {
  const pid = e.attrs?.peer_id
  if (typeof pid !== "string" || pid === "") return ""
  return pid.length > 18 ? `${pid.slice(0, 8)}…${pid.slice(-6)}` : pid
}

/**
 * Live mesh/swarm activity. Streams over /api/network/events SSE when the
 * daemon is up; falls back to polling /api/network/activity every 10s.
 */
export function ActivityPanel() {
  const { t } = useTranslation()
  const [entries, setEntries] = useState<MeshActivityEntry[]>([])
  const [live, setLive] = useState(false)
  const [unavailable, setUnavailable] = useState(false)
  const seen = useRef(new Set<string>())

  useEffect(() => {
    let disposed = false
    let poller: ReturnType<typeof setInterval> | undefined

    const push = (incoming: MeshActivityEntry[]) => {
      setEntries((prev) => {
        const next = [...prev]
        for (const e of incoming) {
          const k = entryKey(e)
          if (seen.current.has(k)) continue
          seen.current.add(k)
          next.push(e)
        }
        if (next.length > MAX_ENTRIES) {
          for (const e of next.slice(0, next.length - MAX_ENTRIES)) {
            seen.current.delete(entryKey(e))
          }
          return next.slice(next.length - MAX_ENTRIES)
        }
        return next
      })
    }

    const startPolling = () => {
      if (poller || disposed) return
      const tick = () =>
        getNetworkActivity(50)
          .then((res) => {
            if (!disposed) push(res.events ?? [])
          })
          .catch(() => {
            if (!disposed) setUnavailable(true)
          })
      tick()
      poller = setInterval(tick, POLL_INTERVAL_MS)
    }

    const src = openNetworkEventStream(
      (e) => push([e]),
      () => {
        if (disposed) return
        setLive(false)
        startPolling()
      },
    )
    if (src) {
      src.onopen = () => {
        if (!disposed) {
          setLive(true)
          setUnavailable(false)
        }
      }
      // Seed the panel with the recent buffer so it isn't empty until the
      // next live event arrives.
      getNetworkActivity(50)
        .then((res) => {
          if (!disposed) push(res.events ?? [])
        })
        .catch(() => {})
    } else {
      startPolling()
    }

    return () => {
      disposed = true
      src?.close()
      if (poller) clearInterval(poller)
    }
  }, [])

  const display = entries.slice().reverse()

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <IconActivity className="size-4" />
          {t("pages.network.activity", "Mesh activity")}
          <Badge variant={live ? "default" : "outline"} className="text-xs">
            {live
              ? t("pages.network.activity_live", "live")
              : t("pages.network.activity_polling", "polling")}
          </Badge>
        </CardTitle>
        <CardDescription>
          {t(
            "pages.network.activity_description",
            "Recent mesh and swarm events observed by the daemon.",
          )}
        </CardDescription>
      </CardHeader>
      <CardContent>
        {unavailable && entries.length === 0 ? (
          <p className="text-muted-foreground py-4 text-center text-sm">
            {t(
              "pages.network.activity_empty",
              "No activity yet. The feed is in-memory on the daemon — start the daemon with mesh enabled.",
            )}
          </p>
        ) : display.length === 0 ? (
          <div className="text-muted-foreground flex items-center justify-center gap-2 py-4 text-sm">
            <IconLoader2 className="size-4 animate-spin" />
            {t("pages.network.activity_waiting", "Waiting for events…")}
          </div>
        ) : (
          <ScrollArea className="h-64">
            <table className="w-full text-xs">
              <tbody>
                {display.map((e) => (
                  <tr key={entryKey(e)} className="border-b last:border-0">
                    <td className="text-muted-foreground py-1.5 pr-3 whitespace-nowrap">
                      {formatTime(e.time)}
                    </td>
                    <td className="py-1.5 pr-3">
                      <Badge
                        variant={kindVariant(e.kind)}
                        className="font-mono"
                      >
                        {e.kind}
                      </Badge>
                    </td>
                    <td className="text-muted-foreground py-1.5 pr-3 font-mono">
                      {peerAttr(e)}
                    </td>
                    <td className="text-muted-foreground py-1.5">{e.source}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </ScrollArea>
        )}
      </CardContent>
    </Card>
  )
}
