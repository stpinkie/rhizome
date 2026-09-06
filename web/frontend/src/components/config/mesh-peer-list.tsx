import { useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import { IconPlus, IconX } from "@tabler/icons-react"

import { Input } from "@/components/ui/input"

// Base58 alphabet (excludes 0, O, I, l).
const base58 = "[A-HJ-NP-Za-km-z1-9]"

// Peer IDs are multibase base58btc-encoded multihashes. The length varies
// by key type (Ed25519 is 53 chars starting with 12D3KooW, secp256k1 is 54
// chars starting with 16Uiu2HAm, RSA is 46+ chars starting with Qm). Allow
// any base58 string of reasonable length rather than assuming Ed25519.
const peerIDPattern = new RegExp(`^${base58}{40,70}$`)

// A minimal multiaddr sanity check. We only parse strings; the backend will
// run the real multiaddr.NewMultiaddr validation. This catches the most
// common typos while accepting the protocols the backend supports.
const bootstrapMultiaddrPattern = new RegExp(
  `^(/[a-z0-9-]+(/[^\\s/]+)+)*/p2p/${base58}{40,70}$`,
)

interface PeerItem {
  id: number
  value: string
}

function splitItems(value: string): PeerItem[] {
  return value.split(/\r?\n/).map((v) => ({ id: nextId(), value: v }))
}

function joinItems(items: PeerItem[]): string {
  return items.map((i) => i.value).join("\n")
}

let idCounter = 0
function nextId(): number {
  idCounter++
  return idCounter
}

function isValidPeerID(value: string): boolean {
  return peerIDPattern.test(value.trim())
}

function isValidBootstrap(value: string): boolean {
  const trimmed = value.trim()
  if (trimmed.length === 0) {
    return true
  }
  return bootstrapMultiaddrPattern.test(trimmed)
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
  const [items, setItems] = useState<PeerItem[]>(() => splitItems(value))

  // Keep the component in sync when the parent changes the value externally
  // (e.g. on first load or when a config update arrives). Avoid re-syncing
  // every keystroke by comparing the current joined value to the new prop.
  useEffect(() => {
    if (value !== joinItems(items)) {
      setItems(splitItems(value))
    }
  }, [value])

  const update = (next: PeerItem[]) => {
    setItems(next)
    onChange(joinItems(next))
  }

  const addItem = () => {
    const next = [...items, { id: nextId(), value: "" }]
    update(next)
    // Focus the new input on the next tick.
    requestAnimationFrame(() => {
      const inputs = containerRef.current?.querySelectorAll("input")
      const last = inputs?.[inputs.length - 1]
      if (last) {
        last.focus()
      }
    })
  }

  const removeItem = (id: number) => {
    update(items.filter((i) => i.id !== id))
  }

  const changeItem = (id: number, raw: string) => {
    update(items.map((i) => (i.id === id ? { ...i, value: raw } : i)))
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

  const containerRef = useRef<HTMLDivElement>(null)

  return (
    <div ref={containerRef} className="space-y-2">
      {items.map((item) => {
        const error = validate(item.value)
        return (
          <div key={item.id} className="flex items-start gap-2">
            <Input
              value={item.value}
              placeholder={placeholder}
              onChange={(e) => changeItem(item.id, e.target.value)}
              className={error ? "border-destructive" : undefined}
            />
            <Button
              type="button"
              variant="outline"
              size="icon"
              onClick={() => removeItem(item.id)}
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
