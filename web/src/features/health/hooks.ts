import { useQuery } from '@tanstack/react-query'
import { api } from '@/api/client'

export interface HealthSample { at: string; state: string; healthy: boolean; latencyMs?: number; message?: string }
export interface HealthHistory { samples: HealthSample[]; uptimePercent: number; since?: string }

export const useHealth = (ws: string, cid: string, state: string) =>
  useQuery({
    // The card's live state is part of the key so a state change refetches immediately.
    queryKey: ['health', ws, cid, state],
    queryFn: () => api.get<HealthHistory>(`/api/workspaces/${ws}/components/${cid}/health?limit=60`),
    refetchInterval: 30_000,
    retry: false,
  })

export const useLogs = (ws: string, cid: string, enabled: boolean, follow: boolean) =>
  useQuery({
    queryKey: ['logs', ws, cid],
    queryFn: async () => (await api.get<{ logs: string }>(`/api/workspaces/${ws}/components/${cid}/logs?tail=300`)).logs,
    enabled,
    refetchInterval: follow ? 3000 : false,
    retry: false,
  })
