import { useMutation, useQuery } from '@tanstack/react-query'
import { api } from '@/api/client'
import type { CanvasComponent, TestResult } from '@/api/types'

export interface ConnectionBody {
  connName: string
  environment: string
  fields: Record<string, unknown>
}

export const STAGES: { key: string; label: string }[] = [
  { key: 'input_validation', label: 'Input validation' },
  { key: 'dns', label: 'DNS resolution' },
  { key: 'network', label: 'Network reachability' },
  { key: 'tls', label: 'TLS handshake' },
  { key: 'authentication', label: 'Authentication' },
  { key: 'authorization', label: 'Authorization' },
  { key: 'server_details', label: 'Server details' },
]

const base = (ws: string, cid: string) => `/api/workspaces/${ws}/components/${cid}/connection`

export const useTestConnection = (ws: string, cid: string) =>
  useMutation({ mutationFn: (b: ConnectionBody) => api.post<TestResult>(`${base(ws, cid)}/test`, b) })

export const useSaveConnection = (ws: string, cid: string) =>
  useMutation({ mutationFn: (b: ConnectionBody) => api.post<CanvasComponent & { exposedAs?: string[] }>(base(ws, cid), b) })

export const useEditConnection = (ws: string, cid: string) =>
  useMutation({ mutationFn: (b: ConnectionBody) => api.put<CanvasComponent & { exposedAs?: string[] }>(base(ws, cid), b) })

export const useRemoveProfile = (ws: string, cid: string) =>
  useMutation({ mutationFn: (env: string) => api.del<unknown>(`${base(ws, cid)}/profiles/${env}`) })

export const useRetest = (ws: string, cid: string) => useMutation({ mutationFn: () => api.post<TestResult>(`${base(ws, cid)}/retest`) })

export const useDisconnect = (ws: string, cid: string) => useMutation({ mutationFn: () => api.del<void>(base(ws, cid)) })

export const useRotate = (ws: string, cid: string) =>
  useMutation({ mutationFn: (secrets: Record<string, string>) => api.post(`${base(ws, cid)}/rotate`, { secrets }) })

export const useParseConnectionString = () =>
  useMutation({ mutationFn: (v: { catalogId: string; uri: string }) => api.post<{ fields: Record<string, unknown> }>('/api/connections/parse', v) })

export function useConnections(ws: string, enabled: boolean, deps: unknown[] = []) {
  return useQuery({
    queryKey: ['connections', ws, ...deps],
    queryFn: async () => (await api.get<{ connections: (CanvasComponent & { exposedAs?: string[] })[] }>(`/api/workspaces/${ws}/connections`)).connections,
    enabled,
  })
}

export interface ImportCandidate {
  source: string
  catalogId: string
  name: string
  fields: Record<string, unknown>
  secrets: string[]
  missing: string[]
  notes?: string[]
}
export interface ImportResponse { format: 'connection-string' | 'env' | 'compose'; candidates: ImportCandidate[]; skipped: { source: string; reason: string }[] }

export const useImportConnections = () =>
  useMutation({ mutationFn: (v: { content: string; catalogId?: string }) => api.post<ImportResponse>('/api/connections/import', v) })
