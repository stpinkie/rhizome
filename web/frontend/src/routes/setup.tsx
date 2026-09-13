import {
  IconCheck,
  IconLoader2,
  IconRefresh,
} from "@tabler/icons-react"
import { createFileRoute, useNavigate } from "@tanstack/react-router"
import * as React from "react"
import { useTranslation } from "react-i18next"

import {
  addModel,
  fetchUpstreamModels,
  getModels,
  setDefaultModel,
  testModelInline,
  type UpstreamModel,
} from "@/api/models"
import { getSetupStatus } from "@/api/setup"
import {
  getProviderCatalog,
  type ProviderCatalogEntry,
} from "@/components/models/provider-registry"
import { PageHeader } from "@/components/page-header"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

function shortModelID(model: string): string {
  const idx = model.lastIndexOf("/")
  return idx >= 0 ? model.slice(idx + 1) : model
}

function SetupPage() {
  const { t } = useTranslation()
  const navigate = useNavigate()

  const [loading, setLoading] = React.useState(true)
  const [alreadySetup, setAlreadySetup] = React.useState(false)
  const [loadError, setLoadError] = React.useState("")
  const [providers, setProviders] = React.useState<ProviderCatalogEntry[]>([])

  const [providerKey, setProviderKey] = React.useState("")
  const [apiKey, setApiKey] = React.useState("")
  const [apiBase, setApiBase] = React.useState("")
  const [model, setModel] = React.useState("")
  const [fetchedModels, setFetchedModels] = React.useState<UpstreamModel[]>([])
  const [fetchingModels, setFetchingModels] = React.useState(false)
  const [testing, setTesting] = React.useState(false)
  const [testOK, setTestOK] = React.useState<boolean | null>(null)
  const [saving, setSaving] = React.useState(false)
  const [error, setError] = React.useState("")

  React.useEffect(() => {
    let cancelled = false
    Promise.all([getSetupStatus(), getModels()])
      .then(([status, modelsRes]) => {
        if (cancelled) return
        setAlreadySetup(!status.needs_setup)
        const catalog = getProviderCatalog(modelsRes.provider_options).filter(
          (p) => p.createAllowed,
        )
        setProviders(catalog)
        if (catalog.length > 0) {
          const first = catalog[0]
          setProviderKey(first.key)
          setApiBase(first.defaultApiBase ?? "")
          setModel(first.commonModels[0] ?? "")
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setLoadError(err instanceof Error ? err.message : String(err))
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [])

  const provider = providers.find((p) => p.key === providerKey)

  const onProviderChange = (key: string) => {
    const next = providers.find((p) => p.key === key)
    setProviderKey(key)
    setApiBase(next?.defaultApiBase ?? "")
    setModel(next?.commonModels[0] ?? "")
    setFetchedModels([])
    setTestOK(null)
    setError("")
  }

  const onFetchModels = async () => {
    if (!provider) return
    setFetchingModels(true)
    setError("")
    try {
      const res = await fetchUpstreamModels({
        provider: provider.key,
        api_key: apiKey || undefined,
        api_base: apiBase || undefined,
      })
      setFetchedModels(res.models)
      if (res.models.length > 0 && !model) {
        setModel(res.models[0].id)
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setFetchingModels(false)
    }
  }

  const onTest = async () => {
    if (!provider || !model) return
    setTesting(true)
    setTestOK(null)
    setError("")
    try {
      const res = await testModelInline({
        provider: provider.key,
        model,
        api_base: apiBase || undefined,
        api_key: apiKey || undefined,
        auth_method: provider.defaultAuthMethod,
      })
      setTestOK(res.success)
      if (!res.success) {
        setError(res.error || t("setup.testFailed"))
      }
    } catch (err) {
      setTestOK(false)
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setTesting(false)
    }
  }

  const canSave =
    !!provider && !!model && (!provider.requiresApiKey || apiKey !== "")

  const onSave = async () => {
    if (!provider || !canSave) return
    setSaving(true)
    setError("")
    const modelName = shortModelID(model)
    try {
      await addModel({
        model_name: modelName,
        provider: provider.key,
        model,
        api_base: apiBase || undefined,
        api_key: apiKey,
        enabled: true,
      })
      await setDefaultModel(modelName)
      await navigate({ to: "/" })
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }

  if (loading) {
    return (
      <div className="flex flex-1 items-center justify-center p-8">
        <IconLoader2 className="size-6 animate-spin" />
      </div>
    )
  }

  if (alreadySetup) {
    return (
      <div className="flex flex-1 items-center justify-center p-8">
        <Card className="w-full max-w-md">
          <CardHeader>
            <CardTitle>{t("setup.alreadyTitle")}</CardTitle>
            <CardDescription>{t("setup.alreadyDesc")}</CardDescription>
          </CardHeader>
          <CardContent>
            <Button onClick={() => void navigate({ to: "/" })}>
              {t("setup.continue")}
            </Button>
          </CardContent>
        </Card>
      </div>
    )
  }

  const modelChoices =
    fetchedModels.length > 0
      ? fetchedModels.map((m) => m.id)
      : (provider?.commonModels ?? [])

  return (
    <div className="flex flex-1 flex-col overflow-y-auto">
      <PageHeader title={t("setup.title")} />
      <div className="flex flex-1 items-start justify-center p-4">
        <Card className="w-full max-w-lg">
          <CardHeader>
            <CardTitle>{t("setup.cardTitle")}</CardTitle>
            <CardDescription>{t("setup.cardDesc")}</CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            {loadError ? (
              <p className="text-destructive text-sm" role="alert">
                {loadError}
              </p>
            ) : null}

            <div className="flex flex-col gap-2">
              <Label htmlFor="setup-provider">{t("setup.provider")}</Label>
              <Select value={providerKey} onValueChange={onProviderChange}>
                <SelectTrigger id="setup-provider">
                  <SelectValue placeholder={t("setup.provider")} />
                </SelectTrigger>
                <SelectContent>
                  {providers.map((p) => (
                    <SelectItem key={p.key} value={p.key}>
                      {p.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>

            {provider && !provider.requiresApiKey ? null : (
              <div className="flex flex-col gap-2">
                <Label htmlFor="setup-api-key">{t("setup.apiKey")}</Label>
                <Input
                  id="setup-api-key"
                  type="password"
                  autoComplete="off"
                  value={apiKey}
                  onChange={(e) => {
                    setApiKey(e.target.value)
                    setTestOK(null)
                  }}
                  placeholder={t("setup.apiKeyPlaceholder")}
                />
              </div>
            )}

            <div className="flex flex-col gap-2">
              <Label htmlFor="setup-api-base">{t("setup.apiBase")}</Label>
              <Input
                id="setup-api-base"
                value={apiBase}
                onChange={(e) => {
                  setApiBase(e.target.value)
                  setTestOK(null)
                }}
                placeholder={provider?.defaultApiBase ?? "https://"}
              />
              <p className="text-muted-foreground text-xs">
                {t("setup.apiBaseHint")}
              </p>
            </div>

            <div className="flex flex-col gap-2">
              <Label htmlFor="setup-model">{t("setup.model")}</Label>
              <div className="flex gap-2">
                <Input
                  id="setup-model"
                  list="setup-model-options"
                  value={model}
                  onChange={(e) => {
                    setModel(e.target.value)
                    setTestOK(null)
                  }}
                  placeholder={t("setup.modelPlaceholder")}
                />
                {provider?.supportsFetch ? (
                  <Button
                    type="button"
                    variant="outline"
                    size="icon"
                    disabled={fetchingModels}
                    onClick={() => void onFetchModels()}
                    aria-label={t("setup.fetchModels")}
                  >
                    {fetchingModels ? (
                      <IconLoader2 className="size-4 animate-spin" />
                    ) : (
                      <IconRefresh className="size-4" />
                    )}
                  </Button>
                ) : null}
              </div>
              <datalist id="setup-model-options">
                {modelChoices.map((m) => (
                  <option key={m} value={m} />
                ))}
              </datalist>
            </div>

            {testOK === true ? (
              <p className="flex items-center gap-1 text-sm text-green-600">
                <IconCheck className="size-4" />
                {t("setup.testOK")}
              </p>
            ) : null}
            {error ? (
              <p className="text-destructive text-sm" role="alert">
                {error}
              </p>
            ) : null}

            <div className="flex items-center gap-2">
              <Button
                type="button"
                variant="outline"
                disabled={testing || !provider || !model}
                onClick={() => void onTest()}
              >
                {testing ? t("labels.loading") : t("setup.test")}
              </Button>
              <Button
                type="button"
                disabled={!canSave || saving}
                onClick={() => void onSave()}
              >
                {saving ? t("labels.loading") : t("setup.save")}
              </Button>
              <Button
                type="button"
                variant="ghost"
                onClick={() => {
                  sessionStorage.setItem("rhizome.setup.skipped", "1")
                  void navigate({ to: "/" })
                }}
              >
                {t("setup.skip")}
              </Button>
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  )
}

export const Route = createFileRoute("/setup")({
  component: SetupPage,
})
