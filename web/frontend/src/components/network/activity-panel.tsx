import { IconActivity, IconLoader2 } from "@tabler/icons-react"
import { useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"

import {
  type ActivityFilter,
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
import { Input } from "@/components/ui/input"
import { ScrollArea } from "@/components/ui/scroll-area"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

const MAX_ENTRIES = 100
const POLL_INTERVAL_MS = 10000

/** Parse a Go-style duration ("30m", "1h30m", "45s") to milliseconds. */
function durationMs(raw: string): number | undefined {
  const s = raw.trim()
  if (!s) return undefined
  const re = /(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)/g
  const factor: Record<string, number> = {
    ns: 1 / 1e6,
    us: 1 / 1e3,
    µs: 1 / 1e3,
    ms: 1,
    s: 1000,
    m: 60_000,
    h: 3_600_000,
  }
  let total = 0
  let matched = ""
  let m: RegExpExecArray | null
  while ((m = re.exec(s)) !== null) {
    matched += m[0]
    total += parseFloat(m[1]) * factor[m[2]]
  }
  return matched === s ? total : undefined
}

/**
 * Client-side mirror of the daemon's ActivityFilter matching, applied to
 * SSE events (which arrive unfiltered). `since` accepts a duration
 * ("30m") or an RFC3339 timestamp and is evaluated against entry time.
 */
function entryMatches(e: MeshActivityEntry, f: ActivityFilter): boolean {
  if (f.kind) {
    const k = f.kind.trim()
    const ok = k.endsWith(".*")
      ? e.kind.startsWith(k.slice(0, -1))
      : e.kind === k
    if (!ok) return false
  }
  if (f.peer) {
    const p = f.peer.trim()
    if (!JSON.stringify(e.attrs ?? {}).includes(p)) return false
  }
  if (f.swarm) {
    const s = e.attrs?.swarm_id
    if (typeof s !== "string" || s !== f.swarm.trim()) return false
  }
  if (f.since) {
    const raw = f.since.trim()
    const dur = durationMs(raw)
    const cutoff =
      dur !== undefined
        ? Date.now() - dur
        : Number.isNaN(Date.parse(raw))
          ? undefined
          : Date.parse(raw)
    if (cutoff !== undefined) {
      const t = Date.parse(e.time)
      if (!Number.isNaN(t) && t < cutoff) return false
    }
  }
  return true
}

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
  const [filter, setFilter] = useState<ActivityFilter>({})
  const seen = useRef(new Set<string>())
  const filterRef = useRef<ActivityFilter>({})

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
        getNetworkActivity(50, filterRef.current)
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
      (e) => {
        // SSE frames arrive unfiltered — apply the same matching the
        // daemon applies to /network/activity params.
        if (entryMatches(e, filterRef.current)) push([e])
      },
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
      getNetworkActivity(50, filterRef.current)
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

  // Filter changes reset the buffer and re-seed from the daemon — the
  // daemon-side filter decides what the fresh snapshot contains. The ref
  // copy keeps the SSE/poller closures (mounted once) on the current filter.
  useEffect(() => {
    filterRef.current = filter
    seen.current.clear()
    setEntries([])
    let cancelled = false
    getNetworkActivity(50, filter)
      .then((res) => {
        if (cancelled) return
        setUnavailable(false)
        setEntries((prev) => {
          const next = [...prev]
          for (const e of res.events ?? []) {
            const k = entryKey(e)
            if (seen.current.has(k)) continue
            seen.current.add(k)
            next.push(e)
          }
          return next
        })
      })
      .catch(() => {
        if (!cancelled) setUnavailable(true)
      })
    return () => {
      cancelled = true
    }
  }, [filter])

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
        <div className="mb-3 flex flex-wrap items-center gap-2">
          <Select
            value={filter.kind || "all"}
            onValueChange={(v) =>
              setFilter((f) => ({
                ...f,
                kind: v === "all" ? undefined : v,
              }))
            }
          >
            <SelectTrigger className="h-8 w-32 text-xs">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">
                {t("pages.network.activity_filter_all", "all kinds")}
              </SelectItem>
              <SelectItem value="mesh.*">mesh.*</SelectItem>
              <SelectItem value="swarm.*">swarm.*</SelectItem>
              <SelectItem value="web3.*">web3.*</SelectItem>
            </SelectContent>
          </Select>
          <Input
            placeholder={t("pages.network.activity_filter_peer", "peer id…")}
            value={filter.peer ?? ""}
            onChange={(e) =>
              setFilter((f) => ({
                ...f,
                peer: e.target.value || undefined,
              }))
            }
            className="h-8 w-40 font-mono text-xs"
          />
          <Input
            placeholder={t("pages.network.activity_filter_swarm", "swarm id…")}
            value={filter.swarm ?? ""}
            onChange={(e) =>
              setFilter((f) => ({
                ...f,
                swarm: e.target.value || undefined,
              }))
            }
            className="h-8 w-32 font-mono text-xs"
          />
          <Input
            placeholder={t(
              "pages.network.activity_filter_since",
              "since (30m, RFC3339)",
            )}
            value={filter.since ?? ""}
            onChange={(e) =>
              setFilter((f) => ({
                ...f,
                since: e.target.value || undefined,
              }))
            }
            className="h-8 w-36 text-xs"
          />
        </div>
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
