import { createFileRoute } from "@tanstack/react-router"

import { Web3Page } from "@/components/web3/web3-page"

export const Route = createFileRoute("/web3")({
  component: Web3Page,
})
