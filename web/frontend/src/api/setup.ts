import { launcherFetch } from "@/api/http"

// API client for first-run onboarding status.

export interface SetupStatus {
  needs_setup: boolean
  total_models: number
  configured_models: number
  default_model: string
}

export async function getSetupStatus(): Promise<SetupStatus> {
  const res = await launcherFetch("/api/setup/status")
  if (!res.ok) {
    throw new Error(`status ${res.status}`)
  }
  return res.json() as Promise<SetupStatus>
}
