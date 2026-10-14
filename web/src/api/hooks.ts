import { useEffect } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from './client'
import type { Catalog, Host, Workspace } from './types'

export const keys = {
  workspaces: ['workspaces'] as const,
  workspace: (id: string) => ['workspace', id] as const,
  catalog: ['catalog'] as const,
  host: ['host'] as const,
}

export function useWorkspaces() {
  return useQuery({
    queryKey: keys.workspaces,
    queryFn: async () => (await api.get<{ workspaces: Workspace[] }>('/api/workspaces')).workspaces,
    // Provisioning progress also arrives over SSE once a workspace is opened;
    // the dashboard polls while anything is still in flight.
    refetchInterval: (q) => (q.state.data?.some((w) => ['allocating', 'preparing', 'starting'].includes(w.status)) ? 1500 : false),
  })
}

export function useWorkspace(id: string | undefined) {
  return useQuery({
    queryKey: keys.workspace(id ?? ''),
    queryFn: () => api.get<Workspace>(`/api/workspaces/${id}`),
    enabled: !!id,
  })
}

export const useCatalog = () =>
  useQuery({ queryKey: keys.catalog, queryFn: () => api.get<Catalog>('/api/catalog'), staleTime: Infinity })

export const useHost = () => useQuery({ queryKey: keys.host, queryFn: () => api.get<Host>('/api/host') })

export interface CreateWorkspaceInput {
  agent?: import('./types').AgentInput
  name: string
  description: string
  cpu: number
  ramGb: number
  storageGb: number
  gpu: boolean
}

export function useCreateWorkspace() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (in_: CreateWorkspaceInput) => api.post<Workspace>('/api/workspaces', in_),
    onSuccess: (w) => {
      qc.setQueryData(keys.workspace(w.id), w)
      qc.invalidateQueries({ queryKey: keys.workspaces })
    },
  })
}

export function useDeleteWorkspace() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, confirmName }: { id: string; confirmName: string }) =>
      api.del<void>(`/api/workspaces/${id}?confirmName=${encodeURIComponent(confirmName)}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.workspaces }),
  })
}

export function useWorkspaceAction(action: 'start' | 'stop') {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.post(`/api/workspaces/${id}/${action}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.workspaces }),
  })
}

export function useNameAvailable(name: string) {
  return useQuery({
    queryKey: ['name-available', name],
    queryFn: () => api.get<{ available: boolean; reason?: string }>(`/api/workspaces/name-available?name=${encodeURIComponent(name)}`),
    enabled: name.length >= 2,
    staleTime: 5_000,
  })
}

/**
 * Subscribes to the workspace's Server-Sent Events stream and writes updates
 * straight into the query cache, so cards always reflect observed state.
 */
export function useWorkspaceEvents(id: string | undefined, onEvent?: (type: string, data: unknown) => void) {
  const qc = useQueryClient()
  useEffect(() => {
    if (!id) return
    const es = new EventSource(`/api/workspaces/${id}/events`)
    const handle = (type: string) => (e: MessageEvent) => {
      const { data } = JSON.parse(e.data)
      if (type === 'workspace') {
        qc.setQueryData(keys.workspace(id), data)
        qc.invalidateQueries({ queryKey: keys.workspaces })
      }
      if (type === 'wires_changed') { qc.invalidateQueries({ queryKey: ['wires', id] }); qc.invalidateQueries({ queryKey: ['variables', id] }) }
      onEvent?.(type, data)
    }
    for (const t of ['workspace', 'component', 'component_removed', 'operation', 'ports', 'wires_changed']) es.addEventListener(t, handle(t))
    return () => es.close()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id, qc])
}

// --- auth ---

export interface Me { id: string; email: string; name: string; role: string }

export const authKeys = { me: ['me'] as const, status: ['auth-status'] as const }

export const useMe = () =>
  useQuery({ queryKey: authKeys.me, queryFn: async () => (await api.get<{ user: Me }>('/api/auth/me')).user, retry: false, staleTime: Infinity })

export const useAuthStatus = () =>
  useQuery({ queryKey: authKeys.status, queryFn: () => api.get<{ enabled: boolean; signupOpen: boolean }>('/api/auth/status') })

export function useSignIn(mode: 'login' | 'register') {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { email: string; password: string; name?: string }) =>
      api.post<{ user: Me }>(mode === 'login' ? '/api/auth/login' : '/api/auth/register', v),
    onSuccess: (r) => {
      qc.removeQueries({ predicate: (q) => q.queryKey[0] !== 'me' })
      qc.setQueryData(authKeys.me, r.user)
    },
  })
}

export function useSignOut() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => api.post('/api/auth/logout'),
    onSuccess: () => {
      qc.removeQueries({ predicate: (q) => q.queryKey[0] !== 'me' })
      qc.invalidateQueries({ queryKey: authKeys.me })
    },
  })
}
