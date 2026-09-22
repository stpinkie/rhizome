// Shared formatting/variant helpers for the network panels — kept out of
// the component files so react-refresh stays clean.

export function transportVariant(
  transport: string,
): "default" | "secondary" | "outline" {
  switch (transport) {
    case "quic":
      return "default"
    case "relay":
      return "secondary"
    default:
      return "outline"
  }
}

export function formatBytes(n: number): string {
  if (n >= 1 << 30) return `${(n / (1 << 30)).toFixed(1)} GiB`
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MiB`
  if (n >= 1 << 10) return `${(n / (1 << 10)).toFixed(1)} KiB`
  return `${n} B`
}
