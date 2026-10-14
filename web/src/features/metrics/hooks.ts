import { useQuery } from '@tanstack/react-query'
import { api } from '@/api/client'

export interface MetricSample {
  at: string
  cpuPercent: number
  memUsedMb: number
  memLimitMb: number
  /** -1 when it cannot be measured. */
  diskUsedMb: number
  latencyMs?: number
  simulated?: boolean
}
export interface MetricCard {
  id: string
  catalogId: string
  name: string
  mode: 'installed' | 'connected'
  current?: MetricSample
  history: MetricSample[]
  unavailable?: string
}
export interface MetricsSnapshot {
  at: string
  workspace?: MetricCard
  limits: { cpu: number; ramGb: number; storageGb: number }
  components: MetricCard[]
  simulated?: boolean
}

export const metricKeys = { all: (ws: string) => ['metrics', ws] as const }

/** Polls while a card or the resources page is open; there is no long-term retention. */
export function useMetrics(ws: string, enabled = true) {
  return useQuery({
    queryKey: metricKeys.all(ws),
    queryFn: () => api.get<MetricsSnapshot>(`/api/workspaces/${ws}/metrics`),
    enabled,
    refetchInterval: 10_000,
  })
}
