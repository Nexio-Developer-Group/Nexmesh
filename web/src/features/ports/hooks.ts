import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'

export interface PortRow { port: number; label?: string; declared: boolean; detected: boolean; local: boolean; process?: string }
export interface PortsResponse { ports: PortRow[]; online: boolean; previewMode: 'path' | 'subdomain' }

export const portKeys = { list: (ws: string) => ['ports', ws] as const }

export const usePorts = (ws: string) =>
  useQuery({ queryKey: portKeys.list(ws), queryFn: () => api.get<PortsResponse>(`/api/workspaces/${ws}/ports`), retry: false })

export function usePortActions(ws: string) {
  const qc = useQueryClient()
  const refresh = () => qc.invalidateQueries({ queryKey: portKeys.list(ws) })
  return {
    declare: useMutation({ mutationFn: (v: { port: number; label: string }) => api.put<void>(`/api/workspaces/${ws}/ports/${v.port}`, { label: v.label }), onSuccess: refresh }),
    remove: useMutation({ mutationFn: (port: number) => api.del<void>(`/api/workspaces/${ws}/ports/${port}`), onSuccess: refresh }),
    preview: useMutation({ mutationFn: (port: number) => api.post<{ url: string; expiresAt: string; mode: string }>(`/api/workspaces/${ws}/ports/${port}/preview`) }),
  }
}

export interface ShareLink { id: string; port: number; label?: string; createdAt: string; expiresAt: string; revoked: boolean; active: boolean; hits: number }
export interface SharesResponse { enabled: boolean; links: ShareLink[] }

export const useShares = (ws: string) =>
  useQuery({ queryKey: ['shares', ws], queryFn: () => api.get<SharesResponse>(`/api/workspaces/${ws}/shares`), retry: false })

export function useShareActions(ws: string) {
  const qc = useQueryClient()
  const refresh = () => qc.invalidateQueries({ queryKey: ['shares', ws] })
  return {
    create: useMutation({
      mutationFn: (v: { port: number; ttlMinutes: number; label: string }) => api.post<{ link: ShareLink; url: string }>(`/api/workspaces/${ws}/shares`, v),
      onSuccess: refresh,
    }),
    revoke: useMutation({ mutationFn: (id: string) => api.del<void>(`/api/workspaces/${ws}/shares/${id}`), onSuccess: refresh }),
  }
}
