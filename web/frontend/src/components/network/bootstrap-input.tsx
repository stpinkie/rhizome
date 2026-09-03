import { IconCheck } from "@tabler/icons-react"
import { useState } from "react"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"

function parseBootstraps(raw: string): string[] {
  return raw
    .split(/[\n,]+/)
    .map((s) => s.trim())
    .filter((s) => s.length > 0)
}

interface BootstrapInputProps {
  bootstraps: string[]
  onChange: (bootstraps: string[]) => void
  onApply: () => void
  disabled?: boolean
}

export function BootstrapInput({
  bootstraps,
  onChange,
  onApply,
  disabled,
}: BootstrapInputProps) {
  const { t } = useTranslation()
  const [draft, setDraft] = useState(bootstraps.join("\n"))

  const handleApply = () => {
    const parsed = parseBootstraps(draft)
    onChange(parsed)
    onApply()
  }

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <Label htmlFor="network-bootstrap">
          {t("pages.network.bootstrap_label", "Bootstrap peers (optional)")}
        </Label>
        <Button
          variant="outline"
          size="sm"
          onClick={handleApply}
          disabled={disabled}
        >
          <IconCheck className="size-4" />
          {t("pages.network.apply", "Apply")}
        </Button>
      </div>
      <Textarea
        id="network-bootstrap"
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        placeholder={t(
          "pages.network.bootstrap_placeholder",
          "/ip4/127.0.0.1/tcp/4001/p2p/12D3...",
        )}
        disabled={disabled}
        rows={3}
      />
    </div>
  )
}
