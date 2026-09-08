import { createFileRoute } from "@tanstack/react-router"

import { BrowserPage } from "@/components/browser/browser-page"

export const Route = createFileRoute("/browser")({
  component: BrowserPage,
})
