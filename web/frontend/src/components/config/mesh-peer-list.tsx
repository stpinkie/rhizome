import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import { IconPlus, IconX } from "@tabler/icons-react"

import { Input } from "@/components/ui/input"

function splitLines(value: string): string[] {
  return value.split(/\r?\n/).map((s) => s.trim())
}

function joinLines(lines: string[]): string {
  return lines.join("\n")
}

const peerIDPattern = /^12D3KooW[A-HJ-NP-Za-km-z1-9]{44}$/

function isValidPeerID(value: string): boolean {
  return peerIDPattern.test(value.trim())
}

function isValidBootstrap(value: string): boolean {
  const trimmed = value.trim()
  if (trimmed.length === 0) {
    return true
  }
  const parts = trimmed.split("/p2p/")
  if (parts.length !== 2 || parts[1].trim() === "") {
    return false
  }
  return peerIDPattern.test(parts[1].trim())
}

interface MeshPeerListProps {
  value: string
  onChange: (value: string) => void
  placeholder?: string
  kind: "peer" | "bootstrap"
}

export function MeshPeerList({
  value,
  onChange,
  placeholder,
  kind,
}: MeshPeerListProps) {
  const { t } = useTranslation()
  const items = splitLines(value).filter((s) => s.length > 0)

  const update = (next: string[]) => {
    onChange(joinLines(next))
  }

  const addItem = () => {
    update([...items, ""])
  }

  const removeItem = (index: number) => {
    const next = [...items]
    next.splice(index, 1)
    update(next)
  }

  const changeItem = (index: number, raw: string) => {
    const next = [...items]
    next[index] = raw
    update(next)
  }

  const validate = (raw: string): string | null => {
    const trimmed = raw.trim()
    if (trimmed.length === 0) {
      return null
    }
    if (kind === "peer") {
      return isValidPeerID(trimmed)
        ? null
        : t("pages.config.mesh_peer_id_invalid", "Invalid peer ID")
    }
    return isValidBootstrap(trimmed)
      ? null
      : t("pages.config.mesh_bootstrap_invalid", "Invalid bootstrap multiaddr")
  }

  return (
    <div className="space-y-2">
      {items.map((item, i) => {
        const error = validate(item)
        return (
          <div key={i} className="flex items-start gap-2">
            <Input
              value={item}
              placeholder={placeholder}
              onChange={(e) => changeItem(i, e.target.value)}
              className={error ? "border-destructive" : undefined}
            />
            <Button
              type="button"
              variant="outline"
              size="icon"
              onClick={() => removeItem(i)}
              aria-label={t("pages.config.mesh_peer_remove", "Remove")}
            >
              <IconX className="size-4" />
            </Button>
            {error && (
              <span className="text-destructive text-xs mt-2">{error}</span>
            )}
          </div>
        )
      })}
      <Button type="button" variant="outline" size="sm" onClick={addItem}>
        <IconPlus className="size-4 mr-1" />
        {t("pages.config.mesh_peer_add", "Add")}
      </Button>
    </div>
  )
}
