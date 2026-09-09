import { IconLoader2, IconPlugConnected, IconPlus } from "@tabler/icons-react"
import { useState } from "react"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { acceptPairBundle, createPairBundle } from "@/api/network"

// PairPanel implements the "Pair a node" flow: mint a single-use bundle to
// share out of band, or redeem a bundle received from another node.
export function PairPanel() {
  const { t } = useTranslation()
  const [bundle, setBundle] = useState("")
  const [created, setCreated] = useState("")
  const [paired, setPaired] = useState("")
  const [busy, setBusy] = useState<"create" | "accept" | "">("")
  const [error, setError] = useState("")

  const create = async () => {
    setBusy("create")
    setError("")
    setPaired("")
    try {
      const res = await createPairBundle()
      setCreated(res.bundle)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy("")
    }
  }

  const accept = async () => {
    setBusy("accept")
    setError("")
    setPaired("")
    try {
      const res = await acceptPairBundle(bundle.trim())
      setPaired(res.peer_id)
      setBundle("")
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setBusy("")
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <IconPlugConnected className="size-4" />
          {t("pages.network.pair", "Pair a Node")}
        </CardTitle>
        <CardDescription>
          {t(
            "pages.network.pair_description",
            "Exchange trust with another node via a single-use signed bundle instead of copying peer ids.",
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="flex items-center gap-2">
          <Button
            size="sm"
            variant="outline"
            disabled={busy !== ""}
            onClick={() => void create()}
          >
            {busy === "create" ? (
              <IconLoader2 className="mr-1 size-4 animate-spin" />
            ) : (
              <IconPlus className="mr-1 size-4" />
            )}
            {t("pages.network.pair_create", "Create invite bundle")}
          </Button>
        </div>
        {created && (
          <div className="bg-muted/40 space-y-1 rounded-md p-3">
            <div className="text-muted-foreground text-xs font-medium">
              {t(
                "pages.network.pair_bundle_hint",
                "Share this bundle out of band — it is single-use.",
              )}
            </div>
            <pre className="text-xs break-all whitespace-pre-wrap">
              {created}
            </pre>
          </div>
        )}
        <div className="flex items-center gap-2">
          <input
            className="border-input bg-background flex-1 rounded-md border px-2 py-1 font-mono text-xs"
            placeholder={t(
              "pages.network.pair_bundle_input",
              "Paste a bundle from the other node",
            )}
            value={bundle}
            onChange={(e) => setBundle(e.target.value)}
          />
          <Button
            size="sm"
            disabled={busy !== "" || bundle.trim() === ""}
            onClick={() => void accept()}
          >
            {busy === "accept" && (
              <IconLoader2 className="mr-1 size-4 animate-spin" />
            )}
            {t("pages.network.pair_accept", "Accept")}
          </Button>
        </div>
        {paired && (
          <p className="text-xs text-green-600">
            {t("pages.network.pair_paired", "Paired with")}{" "}
            <span className="font-mono">{paired}</span>
          </p>
        )}
        {error && <p className="text-destructive text-xs">{error}</p>}
      </CardContent>
    </Card>
  )
}
