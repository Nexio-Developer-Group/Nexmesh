import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'

export interface TerminalInfo { id: string; title: string; exited: boolean; exitCode: number; cols: number; rows: number }

const key = (ws: string) => ['terminals', ws] as const

export const useTerminalList = (ws: string) =>
  useQuery({
    queryKey: key(ws),
    queryFn: async () => (await api.get<{ sessions: TerminalInfo[] }>(`/api/workspaces/${ws}/terminals`)).sessions,
    retry: false,
  })

export function useTerminalActions(ws: string) {
  const qc = useQueryClient()
  const refresh = () => qc.invalidateQueries({ queryKey: key(ws) })
  return {
    open: useMutation({ mutationFn: (v: { cols?: number; rows?: number } = {}) => api.post<TerminalInfo>(`/api/workspaces/${ws}/terminals`, v), onSuccess: refresh }),
    close: useMutation({ mutationFn: (id: string) => api.del<void>(`/api/workspaces/${ws}/terminals/${id}`), onSuccess: refresh }),
  }
}
