import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import { keys } from '@/api/hooks'
import type { CanvasComponent } from '@/api/types'

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
