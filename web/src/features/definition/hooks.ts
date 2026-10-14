import { useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import type { Workspace } from '@/api/types'
import { keys } from '@/api/hooks'

export interface Difference { category: string; subject: string; field?: string; expected: string; actual: string; kind: 'changed' | 'missing' | 'extra' | 'unhealthy' }
export interface DriftReport { inSync: boolean; differences: Difference[] }
export interface Todo { kind: 'credential' | 'secret_variable' | 'agent_key' | 'confirm_install'; subject: string; detail: string }
export interface ImportResult { workspace?: Workspace; todos: Todo[]; dryRun: boolean }

export const useDrift = (ws: string) =>
  useMutation({ mutationFn: (definition: string) => api.post<DriftReport>(`/api/workspaces/${ws}/drift`, { definition }) })

export function useImportDefinition() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { definition: string; name: string; autoInstall: boolean; dryRun: boolean }) => api.post<ImportResult>('/api/workspaces/import', v),
    onSuccess: (r) => { if (!r.dryRun) qc.invalidateQueries({ queryKey: keys.workspaces }) },
  })
}

export const readFileText = (f: File) => f.text()
