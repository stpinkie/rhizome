import { createFileRoute } from "@tanstack/react-router"

import { ModulesPage } from "@/components/modules/modules-page"

export const Route = createFileRoute("/modules")({
  component: ModulesPage,
})
