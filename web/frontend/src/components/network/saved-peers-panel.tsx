import {
  IconCheck,
  IconLoader2,
  IconShieldOff,
  IconTrash,
  IconX,
} from "@tabler/icons-react"
import { useState } from "react"
import { useTranslation } from "react-i18next"

import type { SavedPeer } from "@/api/network"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"

interface CapabilityPillProps {
  label: string
  items?: string[]
}

function CapabilityPill({ label, items }: CapabilityPillProps) {
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

interface SavedPeersPanelProps {
  peers: SavedPeer[]
  isLoading: boolean
  isUntrusting?: boolean
  isRemoving?: boolean
  onUntrust: (peerID: string) => void
  onRemove: (peerID: string) => void
}

export function SavedPeersPanel({
  peers,
  isLoading,
  isUntrusting,
  isRemoving,
  onUntrust,
  onRemove,
}: SavedPeersPanelProps) {
  const { t } = useTranslation()
  const [confirming, setConfirming] = useState<string | null>(null)

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("pages.network.saved_peers", "Saved peers")}</CardTitle>
        <CardDescription>
          {t(
            "pages.network.saved_peers_description",
            "Manage your trusted and bootstrap peers. Untrusted peers remain connectable but cannot run remote tasks.",
          )}
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
            <p>{t("pages.network.no_saved_peers", "No saved peers found.")}</p>
            <p className="mt-1 opacity-70">
              {t(
                "pages.network.no_saved_peers_hint",
                "Add a bootstrap peer with Trust & remember to save it here.",
              )}
            </p>
          </div>
        ) : (
          <div className="space-y-4">
            {peers.map((peer) => (
              <div key={peer.peer_id} className="bg-muted/40 rounded-lg p-4">
                <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="font-mono text-sm break-all">
                        {peer.peer_id}
                      </span>
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
                      {typeof peer.connected === "boolean" && (
                        <Badge
                          variant={peer.connected ? "default" : "outline"}
                          className="shrink-0"
                        >
                          {peer.connected
                            ? t("pages.network.connected", "Connected")
                            : t("pages.network.offline", "Offline")}
                        </Badge>
                      )}
                    </div>

                    {peer.bootstrap_addrs.length > 0 && (
                      <div className="mt-2 space-y-1">
                        <span className="text-muted-foreground text-xs">
                          {t(
                            "pages.network.bootstrap_addrs",
                            "Bootstrap addresses",
                          )}
                        </span>
                        <ul className="space-y-0.5">
                          {peer.bootstrap_addrs.map((addr) => (
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

                    <CapabilityPill
                      label={t("pages.network.models", "Models")}
                      items={peer.capability?.models}
                    />
                    <CapabilityPill
                      label={t("pages.network.skills", "Skills")}
                      items={peer.capability?.skills}
                    />
                    <CapabilityPill
                      label={t("pages.network.agents", "Agents")}
                      items={peer.capability?.agents}
                    />
                  </div>

                  <div className="flex shrink-0 flex-wrap items-center gap-2">
                    {peer.trusted && (
                      <Button
                        variant="outline"
                        size="sm"
                        disabled={isUntrusting}
                        onClick={() => onUntrust(peer.peer_id)}
                      >
                        {isUntrusting ? (
                          <IconLoader2 className="mr-1 size-4 animate-spin" />
                        ) : (
                          <IconShieldOff className="mr-1 size-4" />
                        )}
                        {t("pages.network.untrust", "Untrust")}
                      </Button>
                    )}

                    <AlertDialog
                      open={confirming === peer.peer_id}
                      onOpenChange={(open) =>
                        setConfirming(open ? peer.peer_id : null)
                      }
                    >
                      <AlertDialogTrigger asChild>
                        <Button
                          variant="outline"
                          size="sm"
                          disabled={isRemoving}
                        >
                          {isRemoving ? (
                            <IconLoader2 className="mr-1 size-4 animate-spin" />
                          ) : (
                            <IconTrash className="mr-1 size-4" />
                          )}
                          {t("pages.network.remove", "Remove")}
                        </Button>
                      </AlertDialogTrigger>
                      <AlertDialogContent>
                        <AlertDialogHeader>
                          <AlertDialogTitle>
                            {t(
                              "pages.network.remove_peer",
                              "Remove saved peer?",
                            )}
                          </AlertDialogTitle>
                          <AlertDialogDescription>
                            {t(
                              "pages.network.remove_peer_description",
                              "This will remove the peer from your trusted list and delete any saved bootstrap addresses. This action cannot be undone.",
                            )}
                          </AlertDialogDescription>
                        </AlertDialogHeader>
                        <AlertDialogFooter>
                          <AlertDialogCancel
                            onClick={() => setConfirming(null)}
                          >
                            {t("common.cancel", "Cancel")}
                          </AlertDialogCancel>
                          <AlertDialogAction
                            onClick={() => {
                              onRemove(peer.peer_id)
                              setConfirming(null)
                            }}
                          >
                            {t("pages.network.remove", "Remove")}
                          </AlertDialogAction>
                        </AlertDialogFooter>
                      </AlertDialogContent>
                    </AlertDialog>
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  )
}
