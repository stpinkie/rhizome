import { IconLoader2 } from "@tabler/icons-react"
import { useTranslation } from "react-i18next"

import type { MeshAuditEntry } from "@/api/network"
import { Badge } from "@/components/ui/badge"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { useNetworkAudit } from "@/hooks/use-network-tasks"

function statusVariant(
  status?: string,
): "default" | "secondary" | "outline" | "destructive" {
  switch (status) {
    case "ok":
    case "done":
      return "default"
    case "rejected":
    case "error":
      return "destructive"
    default:
      return "secondary"
  }
}

function formatTime(ts?: string): string {
  if (!ts) return ""
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return ts
  return d.toLocaleTimeString()
}

function shortID(id?: string): string {
  if (!id) return ""
  return id.length > 16 ? `${id.slice(0, 8)}…${id.slice(-6)}` : id
}

export function AuditPanel() {
  const { t } = useTranslation()
  const query = useNetworkAudit(50)
  const entries = (query.data?.entries ?? []).slice().reverse()

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          {t("pages.network.audit", "Mesh audit log")}
          {query.isFetching && (
            <IconLoader2 className="text-muted-foreground size-4 animate-spin" />
          )}
        </CardTitle>
        <CardDescription>
          {t(
            "pages.network.audit_description",
            "Recent remote operations recorded by the daemon's mesh audit trail.",
          )}
        </CardDescription>
      </CardHeader>
      <CardContent>
        {query.isLoading ? (
          <div className="space-y-2">
            <Skeleton className="h-8 w-full" />
            <Skeleton className="h-8 w-full" />
          </div>
        ) : query.error ? (
          <p className="text-destructive text-sm">
            {query.error instanceof Error
              ? query.error.message
              : String(query.error)}
          </p>
        ) : entries.length === 0 ? (
          <p className="text-muted-foreground py-4 text-center text-sm">
            {t(
              "pages.network.audit_empty",
              "No audit entries yet. Enable mesh.audit_log and run a remote task to populate the trail.",
            )}
          </p>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-xs">
              <thead>
                <tr className="text-muted-foreground border-b text-left">
                  <th className="py-1.5 pr-3 font-medium">
                    {t("pages.network.audit_time", "Time")}
                  </th>
                  <th className="py-1.5 pr-3 font-medium">
                    {t("pages.network.audit_op", "Op")}
                  </th>
                  <th className="py-1.5 pr-3 font-medium">
                    {t("pages.network.audit_status", "Status")}
                  </th>
                  <th className="py-1.5 pr-3 font-medium">
                    {t("pages.network.audit_peer", "Peer")}
                  </th>
                  <th className="py-1.5 pr-3 font-medium">
                    {t("pages.network.audit_agent", "Agent")}
                  </th>
                  <th className="py-1.5 pr-3 font-medium">
                    {t("pages.network.audit_ref", "Ref")}
                  </th>
                  <th className="py-1.5 pr-3 font-medium">
                    {t("pages.network.audit_duration", "Duration")}
                  </th>
                  <th className="py-1.5 font-medium">
                    {t("pages.network.audit_detail", "Detail")}
                  </th>
                </tr>
              </thead>
              <tbody>
                {entries.map((e: MeshAuditEntry, i: number) => (
                  <tr key={i} className="border-b last:border-0">
                    <td className="py-1.5 pr-3 whitespace-nowrap">
                      {formatTime(e.ts)}
                    </td>
                    <td className="py-1.5 pr-3 font-mono">{e.op}</td>
                    <td className="py-1.5 pr-3">
                      <Badge variant={statusVariant(e.status)}>
                        {e.status}
                      </Badge>
                    </td>
                    <td className="py-1.5 pr-3 font-mono" title={e.peer_id}>
                      {shortID(e.peer_id)}
                    </td>
                    <td className="py-1.5 pr-3">{e.agent_id}</td>
                    <td className="py-1.5 pr-3 font-mono" title={e.ref}>
                      {shortID(e.ref)}
                    </td>
                    <td className="py-1.5 pr-3 whitespace-nowrap">
                      {typeof e.duration_ms === "number"
                        ? `${e.duration_ms}ms`
                        : ""}
                    </td>
                    <td className="text-muted-foreground py-1.5 break-all">
                      {e.detail}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </CardContent>
    </Card>
  )
}
