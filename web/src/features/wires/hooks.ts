import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import type { Wire, WirePreview } from '@/api/types'
import { varKeys } from '@/features/variables/hooks'

export const wireKeys = { list: (ws: string) => ['wires', ws] as const }

export function useWires(ws: string | undefined) {
  return useQuery({
    queryKey: wireKeys.list(ws ?? ''),
    queryFn: async () => (await api.get<{ wires: Wire[] }>(`/api/workspaces/${ws}/wires`)).wires,
    enabled: !!ws,
    refetchInterval: 20_000, // line state follows health checks even if an event is missed
  })
}

export interface WireRequest { fromId: string; toId: string; name?: string; environment?: string }

export function usePreviewWire(ws: string) {
  return useMutation({ mutationFn: (v: WireRequest) => api.post<WirePreview>(`/api/workspaces/${ws}/wires/preview`, v) })
}

export function useCreateWire(ws: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: WireRequest) => api.post<Wire>(`/api/workspaces/${ws}/wires`, { ...v, confirm: true }),
    onSuccess: () => { qc.invalidateQueries({ queryKey: wireKeys.list(ws) }); qc.invalidateQueries({ queryKey: varKeys.list(ws) }) },
  })
}

export function useDeleteWire(ws: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.del<void>(`/api/workspaces/${ws}/wires/${id}`),
    onSuccess: () => { qc.invalidateQueries({ queryKey: wireKeys.list(ws) }); qc.invalidateQueries({ queryKey: varKeys.list(ws) }) },
  })
}
