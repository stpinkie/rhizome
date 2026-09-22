import { IconCheck, IconRefresh, IconWallet, IconX } from "@tabler/icons-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useState } from "react"
import { useTranslation } from "react-i18next"

import {
  type PendingEntry,
  getWeb3Pending,
  getWeb3Wallet,
  resolveWeb3Approval,
} from "@/api/web3"
import { PageHeader } from "@/components/page-header"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
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

function statusBadge(status: PendingEntry["status"]) {
  switch (status) {
    case "pending":
      return <Badge variant="secondary">pending</Badge>
    case "approved":
      return <Badge variant="default">approved</Badge>
    case "sent":
    case "done":
      return <Badge variant="default">{status}</Badge>
    case "rejected":
    case "expired":
      return <Badge variant="outline">{status}</Badge>
    case "failed":
      return <Badge variant="destructive">failed</Badge>
    default:
      return <Badge variant="outline">{status}</Badge>
  }
}

export function Web3Page() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [confirming, setConfirming] = useState<PendingEntry | null>(null)
  const [message, setMessage] = useState<string | null>(null)

  const pendingQuery = useQuery({
    queryKey: ["web3", "pending"],
    queryFn: getWeb3Pending,
    refetchInterval: 10000,
    staleTime: 3000,
    retry: 1,
  })
  const walletQuery = useQuery({
    queryKey: ["web3", "wallet"],
    queryFn: getWeb3Wallet,
    staleTime: 15000,
    retry: 1,
  })

  const resolveMut = useMutation({
    mutationFn: ({ id, action }: { id: string; action: "approve" | "reject" }) =>
      resolveWeb3Approval(id, action),
    onSuccess: (entry) => {
      setMessage(
        entry.status === "sent"
          ? `Broadcast ${entry.tx_hash}`
          : entry.status === "done"
            ? `Signed: ${entry.result}`
            : `${entry.id}: ${entry.status}`,
      )
      setConfirming(null)
      queryClient.invalidateQueries({ queryKey: ["web3"] })
    },
    onError: (err) => setMessage(err.message),
  })

  const unavailable =
    pendingQuery.error != null &&
    /disabled|unavailable|not available/i.test(pendingQuery.error.message)
  const pending = pendingQuery.data?.pending ?? []
  const awaiting = pending.filter((e) => e.status === "pending")
  const history = pending.filter((e) => e.status !== "pending").slice(0, 20)

  return (
    <div className="flex h-full flex-col">
      <PageHeader title={t("web3.title", "Web3")}>
        <Button
          variant="outline"
          size="sm"
          onClick={() => {
            pendingQuery.refetch()
            walletQuery.refetch()
          }}
        >
          <IconRefresh className="mr-1 size-4" />
          {t("common.refresh", "Refresh")}
        </Button>
      </PageHeader>
      <div className="flex-1 space-y-4 overflow-auto p-6">
        {message && (
          <p className="text-muted-foreground text-sm">{message}</p>
        )}
        {unavailable && (
          <Card>
            <CardContent className="pt-6">
              <p className="text-muted-foreground text-sm">
                {t(
                  "web3.disabled_hint",
                  "Web3 tools are disabled. Set tools.web3.enabled and " +
                    "tools.web3.signing.enabled in config, then restart the daemon.",
                )}
              </p>
            </CardContent>
          </Card>
        )}

        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <IconWallet className="size-4" />
              {t("web3.wallet", "Wallet")}
            </CardTitle>
            <CardDescription>
              {t(
                "web3.wallet_desc",
                "Local signing keys — private material never leaves the keystore.",
              )}
            </CardDescription>
          </CardHeader>
          <CardContent>
            {(walletQuery.data?.addresses ?? []).length === 0 ? (
              <p className="text-muted-foreground text-sm">
                {t(
                  "web3.no_keys",
                  "No keys yet — run `rhizome wallet create`.",
                )}
              </p>
            ) : (
              <ul className="space-y-1">
                {walletQuery.data?.addresses.map((a) => (
                  <li
                    key={a.address}
                    className="flex items-center gap-2 font-mono text-sm"
                  >
                    {a.address}
                    {a.address === walletQuery.data?.default && (
                      <Badge variant="secondary">default</Badge>
                    )}
                    {a.label && (
                      <span className="text-muted-foreground">{a.label}</span>
                    )}
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>
              {t("web3.approvals", "Pending approvals")}
              {awaiting.length > 0 && (
                <Badge variant="destructive" className="ml-2">
                  {awaiting.length}
                </Badge>
              )}
            </CardTitle>
            <CardDescription>
              {t(
                "web3.approvals_desc",
                "Agent-requested signatures — approving signs and broadcasts immediately.",
              )}
            </CardDescription>
          </CardHeader>
          <CardContent>
            {awaiting.length === 0 ? (
              <p className="text-muted-foreground text-sm">
                {t("web3.none_pending", "Nothing awaiting approval.")}
              </p>
            ) : (
              <ul className="space-y-3">
                {awaiting.map((e) => (
                  <li
                    key={e.id}
                    className="border-border/60 flex flex-col gap-2 rounded-lg border p-3"
                  >
                    <div className="flex items-center justify-between gap-2">
                      <span className="text-sm">{e.summary}</span>
                      <div className="flex shrink-0 gap-2">
                        <Button
                          size="sm"
                          onClick={() => setConfirming(e)}
                          disabled={resolveMut.isPending}
                        >
                          <IconCheck className="mr-1 size-4" />
                          {t("web3.approve", "Approve")}
                        </Button>
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() =>
                            resolveMut.mutate({ id: e.id, action: "reject" })
                          }
                          disabled={resolveMut.isPending}
                        >
                          <IconX className="mr-1 size-4" />
                          {t("web3.reject", "Reject")}
                        </Button>
                      </div>
                    </div>
                    <div className="text-muted-foreground flex gap-3 font-mono text-xs">
                      <span>{e.id}</span>
                      <span>{e.kind}</span>
                      <span>chain {e.chain_id}</span>
                      <span>
                        {t("web3.expires", "expires")}{" "}
                        {new Date(e.expires_at).toLocaleTimeString()}
                      </span>
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>

        {history.length > 0 && (
          <Card>
            <CardHeader>
              <CardTitle>{t("web3.history", "Recent")}</CardTitle>
            </CardHeader>
            <CardContent>
              <ul className="space-y-2">
                {history.map((e) => (
                  <li
                    key={e.id}
                    className="flex items-center justify-between gap-2 text-sm"
                  >
                    <span className="truncate">{e.summary}</span>
                    <span className="flex shrink-0 items-center gap-2">
                      {e.tx_hash && (
                        <span className="text-muted-foreground font-mono text-xs">
                          {e.tx_hash.slice(0, 10)}…
                        </span>
                      )}
                      {statusBadge(e.status)}
                    </span>
                  </li>
                ))}
              </ul>
            </CardContent>
          </Card>
        )}
      </div>

      <AlertDialog
        open={confirming != null}
        onOpenChange={(open) => !open && setConfirming(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t("web3.confirm_title", "Approve and broadcast?")}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {confirming?.summary}
              <br />
              {t(
                "web3.confirm_body",
                "This signs with the wallet key and broadcasts to the network. It cannot be undone.",
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel", "Cancel")}</AlertDialogCancel>
            <AlertDialogAction
              onClick={() =>
                confirming &&
                resolveMut.mutate({ id: confirming.id, action: "approve" })
              }
            >
              {t("web3.approve", "Approve")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
