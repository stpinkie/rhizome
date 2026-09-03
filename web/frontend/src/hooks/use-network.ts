import { useQuery } from "@tanstack/react-query"
import { useCallback, useEffect, useMemo, useState } from "react"

import {
  type NetworkStatusOptions,
  getNetworkStatus,
} from "@/api/network"

function usePageVisible() {
  const [visible, setVisible] = useState(
    typeof document === "undefined" || !document.hidden,
  )

  useEffect(() => {
    const handle = () => {
      setVisible(!document.hidden)
    }
    document.addEventListener("visibilitychange", handle)
    return () => document.removeEventListener("visibilitychange", handle)
  }, [])

  return visible
}

export function useNetwork(options?: NetworkStatusOptions) {
  const visible = usePageVisible()
  const memoOptions = useMemo(
    () => ({
      bootstraps: options?.bootstraps?.filter((b) => b.trim() !== ""),
      timeout: options?.timeout,
      listen: options?.listen?.filter((l) => l.trim() !== ""),
    }),
    [options?.bootstraps, options?.listen, options?.timeout],
  )

  const statusQuery = useQuery({
    queryKey: ["network", "status", memoOptions],
    queryFn: () => getNetworkStatus(memoOptions),
    refetchInterval: visible ? 60000 : false,
    staleTime: 5000,
    retry: 1,
  })

  const refresh = useCallback(() => {
    void statusQuery.refetch()
  }, [statusQuery])

  return { statusQuery, refresh }
}
