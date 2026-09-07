import {
  IconCrown,
  IconLoader2,
  IconLogout,
  IconPlayerPlay,
  IconPlus,
  IconSend,
} from "@tabler/icons-react"
import { useState } from "react"
import { useTranslation } from "react-i18next"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { useSwarmOffers, useSwarms } from "@/hooks/use-swarms"
import type { SwarmInfo } from "@/api/network"

function shortID(id?: string): string {
  if (!id) return ""
  return id.length > 16 ? `${id.slice(0, 8)}…${id.slice(-6)}` : id
}

function formatSeen(ts?: string): string {
  if (!ts) return ""
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return ts
  return d.toLocaleTimeString()
}

function offerVariant(
  status?: string,
): "default" | "secondary" | "outline" | "destructive" {
  switch (status) {
    case "assigned":
      return "default"
    case "open":
    case "observed":
      return "secondary"
    case "failed":
    case "expired":
      return "destructive"
    default:
      return "outline"
  }
}

interface SwarmCardProps {
  swarm: SwarmInfo
  selfID?: string
  onLeave: (id: string) => void
  isLeaving: boolean
}

function SwarmCard({ swarm, selfID, onLeave, isLeaving }: SwarmCardProps) {
  const { t } = useTranslation()
  const { query: offersQuery, offer, run } = useSwarmOffers(swarm.id)
  const [taskText, setTaskText] = useState("")
  const [taskAgent, setTaskAgent] = useState("main")
  const [goalText, setGoalText] = useState("")
  const [showOffers, setShowOffers] = useState(false)

  const offers = offersQuery.data?.offers ?? []

  return (
    <div className="bg-muted/40 space-y-3 rounded-lg p-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <span className="font-mono text-sm font-medium">{swarm.id}</span>
          <Badge variant={swarm.joined ? "default" : "secondary"}>
            {swarm.joined
              ? t("pages.network.swarm_joined", "Joined")
              : t("pages.network.swarm_known", "Known")}
          </Badge>
          {swarm.coordinator && (
            <Badge variant="outline" className="gap-1">
              <IconCrown className="size-3" />
              {swarm.coordinator === selfID
                ? t("pages.network.swarm_you_coordinate", "You coordinate")
                : shortID(swarm.coordinator)}
            </Badge>
          )}
          <Badge variant="secondary">
            {swarm.members?.length ?? 0}{" "}
            {t("pages.network.swarm_members", "members")}
          </Badge>
        </div>
        {swarm.joined && (
          <Button
            variant="outline"
            size="sm"
            disabled={isLeaving}
            onClick={() => onLeave(swarm.id)}
          >
            <IconLogout className="mr-1 size-4" />
            {t("pages.network.swarm_leave", "Leave")}
          </Button>
        )}
      </div>

      {(swarm.members?.length ?? 0) > 0 && (
        <div className="space-y-1">
          {(swarm.members ?? []).map((m) => (
            <div
              key={m.peer_id}
              className="flex flex-wrap items-center gap-2 text-xs"
            >
              <span className="font-mono break-all">{m.peer_id}</span>
              {m.peer_id === swarm.coordinator && (
                <IconCrown className="text-muted-foreground size-3" />
              )}
              <Badge variant="outline" className="text-xs">
                {m.source === "direct"
                  ? t("pages.network.swarm_direct", "direct")
                  : t("pages.network.swarm_gossip", "gossip")}
              </Badge>
              {typeof m.active_tasks === "number" && m.active_tasks > 0 && (
                <span className="text-muted-foreground">
                  {m.active_tasks}{" "}
                  {t("pages.network.swarm_active_tasks", "active")}
                </span>
              )}
              <span className="text-muted-foreground">
                {t("pages.network.swarm_last_seen", "seen")}{" "}
                {formatSeen(m.last_seen)}
              </span>
            </div>
          ))}
        </div>
      )}

      {swarm.joined && (
        <div className="space-y-2 border-t pt-3">
          <div className="flex flex-col gap-2 sm:flex-row">
            <Input
              placeholder={t(
                "pages.network.swarm_offer_task",
                "Task to offer (e.g. review this diff)",
              )}
              value={taskText}
              onChange={(e) => setTaskText(e.target.value)}
              className="flex-1"
            />
            <Input
              placeholder={t("pages.network.swarm_offer_agent", "agent id")}
              value={taskAgent}
              onChange={(e) => setTaskAgent(e.target.value)}
              className="sm:w-32"
            />
            <Button
              variant="outline"
              size="sm"
              disabled={offer.isPending || taskText.trim() === ""}
              onClick={() =>
                offer.mutate(
                  { agent_id: taskAgent || "main", task: taskText.trim() },
                  { onSuccess: () => setTaskText("") },
                )
              }
            >
              {offer.isPending ? (
                <IconLoader2 className="mr-1 size-4 animate-spin" />
              ) : (
                <IconSend className="mr-1 size-4" />
              )}
              {t("pages.network.swarm_offer", "Offer")}
            </Button>
          </div>

          <div className="flex gap-2">
            <Input
              placeholder={t(
                "pages.network.swarm_run_goal",
                "Goal for the swarm (decomposed + orchestrated)",
              )}
              value={goalText}
              onChange={(e) => setGoalText(e.target.value)}
              className="flex-1"
            />
            <Button
              size="sm"
              disabled={run.isPending || goalText.trim() === ""}
              onClick={() =>
                run.mutate(
                  { goal: goalText.trim(), agent_id: taskAgent || "main" },
                  { onSuccess: () => setGoalText("") },
                )
              }
            >
              {run.isPending ? (
                <IconLoader2 className="mr-1 size-4 animate-spin" />
              ) : (
                <IconPlayerPlay className="mr-1 size-4" />
              )}
              {t("pages.network.swarm_run", "Run")}
            </Button>
          </div>

          {run.data && (
            <div className="bg-background rounded-md p-3 text-xs">
              <div className="text-muted-foreground mb-1 font-medium">
                {t("pages.network.swarm_run_summary", "Run summary")}
              </div>
              <pre className="whitespace-pre-wrap">{run.data.summary}</pre>
            </div>
          )}
          {run.error && (
            <p className="text-destructive text-xs">
              {run.error instanceof Error
                ? run.error.message
                : String(run.error)}
            </p>
          )}

          <button
            type="button"
            className="text-muted-foreground text-xs underline"
            onClick={() => setShowOffers((v) => !v)}
          >
            {showOffers
              ? t("pages.network.swarm_hide_offers", "Hide offers")
              : t("pages.network.swarm_show_offers", "Show offers")}{" "}
            ({offers.length})
          </button>
          {showOffers && (
            <div className="space-y-1">
              {offers.length === 0 ? (
                <p className="text-muted-foreground text-xs">
                  {t("pages.network.swarm_no_offers", "No tracked offers.")}
                </p>
              ) : (
                offers.map((o) => (
                  <div
                    key={o.offer_id}
                    className="flex flex-wrap items-center gap-2 text-xs"
                  >
                    <span className="font-mono">{shortID(o.offer_id)}</span>
                    <Badge variant={offerVariant(o.status)}>{o.status}</Badge>
                    <span className="text-muted-foreground break-all">
                      {o.task}
                    </span>
                    {o.assignee && (
                      <span className="font-mono">
                        → {shortID(o.assignee)}
                      </span>
                    )}
                    {o.error && (
                      <span className="text-destructive">{o.error}</span>
                    )}
                  </div>
                ))
              )}
            </div>
          )}
        </div>
      )}
    </div>
  )
}

export function SwarmsPanel() {
  const { t } = useTranslation()
  const { query, join, leave } = useSwarms()
  const [newSwarm, setNewSwarm] = useState("")

  const swarms = query.data?.swarms ?? []

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          {t("pages.network.swarms", "Swarms")}
          {query.isFetching && (
            <IconLoader2 className="text-muted-foreground size-4 animate-spin" />
          )}
        </CardTitle>
        <CardDescription>
          {t(
            "pages.network.swarms_description",
            "Named groups of trusted peers that share presence and distribute work.",
          )}
        </CardDescription>
      </CardHeader>
      <CardContent>
        <div className="mb-4 flex gap-2">
          <Input
            placeholder={t(
              "pages.network.swarm_join_placeholder",
              "Swarm name to join",
            )}
            value={newSwarm}
            onChange={(e) => setNewSwarm(e.target.value)}
            className="max-w-xs"
          />
          <Button
            variant="outline"
            size="sm"
            disabled={join.isPending || newSwarm.trim() === ""}
            onClick={() =>
              join.mutate(newSwarm.trim(), {
                onSuccess: () => setNewSwarm(""),
              })
            }
          >
            {join.isPending ? (
              <IconLoader2 className="mr-1 size-4 animate-spin" />
            ) : (
              <IconPlus className="mr-1 size-4" />
            )}
            {t("pages.network.swarm_join", "Join")}
          </Button>
        </div>
        {join.data?.warning && (
          <p className="text-muted-foreground mb-3 text-xs">
            {join.data.warning}
          </p>
        )}
        {join.error && (
          <p className="text-destructive mb-3 text-xs">
            {join.error instanceof Error
              ? join.error.message
              : String(join.error)}
          </p>
        )}

        {query.isLoading ? (
          <div className="space-y-3">
            <Skeleton className="h-16 w-full" />
            <Skeleton className="h-16 w-full" />
          </div>
        ) : query.error ? (
          <p className="text-muted-foreground py-4 text-center text-sm">
            {query.error instanceof Error
              ? query.error.message
              : String(query.error)}
          </p>
        ) : swarms.length === 0 ? (
          <div className="text-muted-foreground py-6 text-center text-sm">
            <p>{t("pages.network.no_swarms", "No swarms yet.")}</p>
            <p className="mt-1 opacity-70">
              {t(
                "pages.network.no_swarms_hint",
                "Join a swarm name above — trusted peers with the same name form a swarm.",
              )}
            </p>
          </div>
        ) : (
          <div className="space-y-4">
            {swarms.map((sw) => (
              <SwarmCard
                key={sw.id}
                swarm={sw}
                selfID={query.data?.peer_id}
                onLeave={(id) => leave.mutate(id)}
                isLeaving={leave.isPending}
              />
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  )
}
