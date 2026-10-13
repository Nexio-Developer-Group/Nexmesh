import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import { keys } from '@/api/hooks'
import type { CanvasComponent, Operation } from '@/api/types'

export const compKeys = {
  list: (wsId: string) => ['components', wsId] as const,
}

export function useComponents(wsId: string | undefined) {
  return useQuery({
    queryKey: compKeys.list(wsId ?? ''),
    queryFn: async () => (await api.get<{ components: CanvasComponent[] }>(`/api/workspaces/${wsId}/components`)).components,
    enabled: !!wsId,
  })
}

/** Applies a live event from the workspace SSE stream to the cached card list. */
export function applyComponentEvent(
  qc: ReturnType<typeof useQueryClient>,
  wsId: string,
  type: string,
  data: unknown,
) {
  if (type === 'component') {
    const c = data as CanvasComponent
    qc.setQueryData<CanvasComponent[]>(compKeys.list(wsId), (old = []) => {
      const i = old.findIndex((x) => x.id === c.id)
      return i === -1 ? [...old, c] : old.map((x) => (x.id === c.id ? c : x))
    })
    qc.invalidateQueries({ queryKey: keys.workspaces })
  } else if (type === 'component_removed') {
    const { id } = data as { id: string }
    qc.setQueryData<CanvasComponent[]>(compKeys.list(wsId), (old = []) => old.filter((x) => x.id !== id))
  }
}

export function useAddComponent(wsId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { catalogId: string; x: number; y: number }) => api.post<CanvasComponent>(`/api/workspaces/${wsId}/components`, v),
    onSuccess: (c) => applyComponentEvent(qc, wsId, 'component', c),
  })
}

export function useMoveComponent(wsId: string) {
  return useMutation({
    mutationFn: (v: { id: string; x: number; y: number }) => api.patch(`/api/workspaces/${wsId}/components/${v.id}`, { x: v.x, y: v.y }),
  })
}

export function useRemoveComponent(wsId: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.del<void>(`/api/workspaces/${wsId}/components/${id}`),
    onSuccess: (_d, id) => applyComponentEvent(qc, wsId, 'component_removed', { id }),
  })
}

// ---- operations, capacity, install lifecycle


export interface Capacity {
  cpu: number; ramMb: number; storageGb: number
  freeCpu: number; freeRamMb: number; freeStorageGb: number
}

export const opKeys = { list: (wsId: string) => ['operations', wsId] as const }

export function useOperations(wsId: string | undefined) {
  return useQuery({
    queryKey: opKeys.list(wsId ?? ''),
    queryFn: async () => (await api.get<{ operations: Operation[] }>(`/api/workspaces/${wsId}/operations`)).operations,
    enabled: !!wsId,
  })
}

export function applyOperationEvent(qc: ReturnType<typeof useQueryClient>, wsId: string, data: unknown) {
  const op = data as Operation
  qc.setQueryData<Operation[]>(opKeys.list(wsId), (old = []) => {
    const i = old.findIndex((x) => x.id === op.id)
    return i === -1 ? [op, ...old] : old.map((x) => (x.id === op.id ? op : x))
  })
  if (op.type === 'install_runtime' || op.type === 'install_component') qc.invalidateQueries({ queryKey: ['workspace', wsId] })
}

export function useCapacity(wsId: string, deps: unknown[] = []) {
  return useQuery({
    queryKey: ['capacity', wsId, ...deps],
    queryFn: () => api.get<Capacity>(`/api/workspaces/${wsId}/capacity`),
  })
}

export interface InstallBody { version: string; settings: Record<string, unknown>; start: boolean; confirm: boolean }

export function useInstallComponent(wsId: string, cid: string) {
  return useMutation({ mutationFn: (b: InstallBody) => api.post<Operation>(`/api/workspaces/${wsId}/components/${cid}/install`, b) })
}

export function useComponentAction(wsId: string, cid: string, action: 'start' | 'stop' | 'retry') {
  return useMutation({ mutationFn: () => api.post<Operation>(`/api/workspaces/${wsId}/components/${cid}/${action}`) })
}

export function useRemoveInstalled(wsId: string, cid: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (deleteData: boolean) => api.del<Operation | void>(`/api/workspaces/${wsId}/components/${cid}?deleteData=${deleteData}`),
    onSuccess: (r) => { if (!r) applyComponentEvent(qc, wsId, 'component_removed', { id: cid }) },
  })
}

export function useRuntimeInstall(wsId: string) {
  return useMutation({
    mutationFn: (v: { name: string; version: string; activate: boolean }) =>
      api.post<Operation>(`/api/workspaces/${wsId}/runtimes/${v.name}/install`, { version: v.version, activate: v.activate }),
  })
}
