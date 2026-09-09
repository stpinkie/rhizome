import { IconLoader2, IconPuzzle } from "@tabler/icons-react"
import { useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { getMCPPresets, updateMCPPreset } from "@/api/tools"

// MCPPresetsCard exposes first-class hosted MCP presets (currently Context7)
// on the tools page: enable/disable + api key entry. Keys are stored as
// SecureString server-side and never echoed back.
export function MCPPresetsCard() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [keyDraft, setKeyDraft] = useState<Record<string, string>>({})
  const [error, setError] = useState("")

  const presets = useQuery({
    queryKey: ["tools", "mcp-presets"],
    queryFn: getMCPPresets,
  })

  const save = useMutation({
    mutationFn: (p: { name: string; enabled?: boolean; api_key?: string }) =>
      updateMCPPreset(p.name, p),
    onSuccess: () => {
      setError("")
      void queryClient.invalidateQueries({ queryKey: ["tools", "mcp-presets"] })
    },
    onError: (e) => setError(e instanceof Error ? e.message : String(e)),
  })

  const items = presets.data?.presets ?? []
  if (!presets.isLoading && items.length === 0 && !presets.error) {
    return null
  }

  return (
    <Card className="mt-6">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <IconPuzzle className="size-4" />
          {t("pages.agent.tools.mcp_presets", "MCP Presets")}
        </CardTitle>
        <CardDescription>
          {t(
            "pages.agent.tools.mcp_presets_desc",
            "First-class hosted MCP servers. Keys are stored encrypted and never shown again.",
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {items.map((p) => (
          <div
            key={p.name}
            className="flex flex-wrap items-center gap-3 rounded-md border p-3"
          >
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2">
                <span className="font-medium">{p.name}</span>
                <Badge variant={p.enabled ? "default" : "secondary"}>
                  {p.enabled
                    ? t("common.enabled", "Enabled")
                    : t("common.disabled", "Disabled")}
                </Badge>
                {p.has_key && (
                  <Badge variant="outline">
                    {t("pages.agent.tools.mcp_key_set", "API key set")}
                  </Badge>
                )}
              </div>
              <div className="text-muted-foreground mt-1 text-xs">
                {p.url} — {t("pages.agent.tools.mcp_header", "header")}:{" "}
                {p.header}
                {!p.has_key && (
                  <>
                    {" "}
                    · {t("pages.agent.tools.mcp_env_fallback", "env fallback")}:{" "}
                    {p.env_var}
                  </>
                )}
              </div>
            </div>
            <input
              type="password"
              className="border-input bg-background rounded-md border px-2 py-1 text-xs"
              placeholder={
                p.has_key
                  ? t("pages.agent.tools.mcp_key_replace", "Replace API key")
                  : t("pages.agent.tools.mcp_key_input", "API key")
              }
              value={keyDraft[p.name] ?? ""}
              onChange={(e) =>
                setKeyDraft((d) => ({ ...d, [p.name]: e.target.value }))
              }
            />
            <Button
              size="sm"
              variant="outline"
              disabled={save.isPending || !(keyDraft[p.name] ?? "").trim()}
              onClick={() => {
                save.mutate({ name: p.name, api_key: keyDraft[p.name] })
                setKeyDraft((d) => ({ ...d, [p.name]: "" }))
              }}
            >
              {t("pages.agent.tools.mcp_save_key", "Save key")}
            </Button>
            <Button
              size="sm"
              variant={p.enabled ? "destructive" : "default"}
              disabled={save.isPending}
              onClick={() => save.mutate({ name: p.name, enabled: !p.enabled })}
            >
              {save.isPending && (
                <IconLoader2 className="mr-1 size-4 animate-spin" />
              )}
              {p.enabled
                ? t("common.disable", "Disable")
                : t("common.enable", "Enable")}
            </Button>
          </div>
        ))}
        {error && <p className="text-destructive text-xs">{error}</p>}
      </CardContent>
    </Card>
  )
}
