import { useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import { keys } from '@/api/hooks'
import type { Environment, Workspace } from '@/api/types'

export const ENVIRONMENTS: Environment[] = ['development', 'staging', 'production']

export function useSwitchEnvironment(ws: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { environment: Environment; confirm?: boolean }) => api.post<Workspace>(`/api/workspaces/${ws}/environment`, v),
    onSuccess: (w) => {
      qc.setQueryData(keys.workspace(ws), w)
      for (const k of ['variables', 'wires', 'components', 'architecture']) qc.invalidateQueries({ queryKey: [k, ws] })
    },
  })
}
