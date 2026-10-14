import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'

export interface Snapshot { id: string; name: string; state: 'creating' | 'ready' | 'failed'; sizeBytes: number; error?: string; createdAt: string; createdBy: string }

const key = (ws: string) => ['snapshots', ws] as const

export const useSnapshots = (ws: string) =>
  useQuery({
    queryKey: key(ws),
    queryFn: () => api.get<{ snapshots: Snapshot[]; max: number }>(`/api/workspaces/${ws}/snapshots`),
    // Creation runs in the background; poll until nothing is in progress.
    refetchInterval: (q) => (q.state.data?.snapshots.some((s) => s.state === 'creating') ? 1500 : false),
    retry: false,
  })

export function useSnapshotActions(ws: string) {
  const qc = useQueryClient()
  const refresh = () => qc.invalidateQueries({ queryKey: key(ws) })
  return {
    create: useMutation({ mutationFn: (name: string) => api.post<Snapshot>(`/api/workspaces/${ws}/snapshots`, { name }), onSuccess: refresh }),
    restore: useMutation({ mutationFn: (id: string) => api.post(`/api/workspaces/${ws}/snapshots/${id}/restore`, { confirm: true }), onSuccess: refresh }),
    remove: useMutation({ mutationFn: (id: string) => api.del<void>(`/api/workspaces/${ws}/snapshots/${id}`), onSuccess: refresh }),
  }
}
