import { IconLoader2, IconRefresh, IconWorld } from "@tabler/icons-react"
import { useState } from "react"
import { useTranslation } from "react-i18next"

import { PageHeader } from "@/components/page-header"
import { Button } from "@/components/ui/button"
import { useNetwork } from "@/hooks/use-network"
import { useSavedPeers } from "@/hooks/use-saved-peers"
import { cn } from "@/lib/utils"

import { BootstrapInput } from "./bootstrap-input"
import { DhtPanel } from "./dht-panel"
import { NodePanel } from "./node-panel"
import { PeersPanel } from "./peers-panel"
import { SavedPeersPanel } from "./saved-peers-panel"

export function NetworkPage() {
  const { t } = useTranslation()
  const [bootstraps, setBootstraps] = useState<string[]>([])
  const [trust, setTrust] = useState(false)
  const { statusQuery, refresh } = useNetwork({ bootstraps, trust })
  const {
    query: savedPeersQuery,
    untrust,
    remove,
  } = useSavedPeers()

  const isLoading = statusQuery.isLoading
  const error = statusQuery.error

  return (
    <div className="flex h-full flex-col">
      <PageHeader
        title={t("navigation.network", "Network")}
        children={
          <Button
            variant="outline"
            size="sm"
            onClick={() => refresh()}
            disabled={isLoading}
          >
            {isLoading ? (
              <IconLoader2 className="size-4 animate-spin" />
            ) : (
              <IconRefresh className="size-4" />
            )}
            {t("pages.network.refresh", "Refresh")}
          </Button>
        }
      />

      <div className="flex-1 overflow-auto px-6 py-6">
        <div className="mx-auto w-full max-w-6xl space-y-6">
          <BootstrapInput
            bootstraps={bootstraps}
            onChange={setBootstraps}
            onApply={() => refresh()}
            trust={trust}
            onTrustChange={setTrust}
            disabled={isLoading}
          />

          {error && (
            <div className="bg-destructive/10 text-destructive rounded-xl p-4 text-sm">
              <div className="flex items-center gap-2 font-medium">
                <IconWorld className="size-4" />
                {t("pages.network.load_error", "Failed to load network status")}
              </div>
              <p className="mt-1 opacity-90">
                {error instanceof Error ? error.message : String(error)}
              </p>
            </div>
          )}

          <div className={cn("grid gap-6", "grid-cols-1 lg:grid-cols-3")}>
            <NodePanel
              response={statusQuery.data}
              isLoading={statusQuery.isLoading}
            />
            <PeersPanel
              response={statusQuery.data}
              isLoading={statusQuery.isLoading}
            />
            <DhtPanel
              response={statusQuery.data}
              isLoading={statusQuery.isLoading}
            />
          </div>

          <SavedPeersPanel
            peers={savedPeersQuery.data?.saved_peers ?? []}
            isLoading={savedPeersQuery.isLoading}
            isUntrusting={untrust.isPending}
            isRemoving={remove.isPending}
            onUntrust={(peerID) => untrust.mutate(peerID)}
            onRemove={(peerID) => remove.mutate(peerID)}
          />
        </div>
      </div>
    </div>
  )
}
