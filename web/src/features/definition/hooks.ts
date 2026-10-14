import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import type { Workspace } from '@/api/types'
import { keys } from '@/api/hooks'

export interface Difference { category: string; subject: string; field?: string; expected: string; actual: string; kind: 'changed' | 'missing' | 'extra' | 'unhealthy' }
export interface DriftReport { inSync: boolean; differences: Difference[] }
export interface Todo { kind: 'credential' | 'secret_variable' | 'agent_key' | 'confirm_install' | 'review_gates'; subject: string; detail: string }
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

export interface ReportItem { field: string; status: 'applied' | 'adapted' | 'unsupported'; detail: string }
export interface DevContainerImport extends ImportResult {
  report: ReportItem[]
  postCreate: string[]
  name: string
  resources: { cpu: number; ramGb: number; storageGb: number }
}

export const useImportDevContainer = () => {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { content: string; name: string; autoInstall: boolean; runPostCreate: boolean; dryRun: boolean }) => api.post<DevContainerImport>('/api/devcontainer/import', v),
    onSuccess: (r) => { if (!r.dryRun) qc.invalidateQueries({ queryKey: keys.workspaces }) },
  })
}

export const useDevContainerReport = (ws: string, enabled: boolean) =>
  useQuery({
    queryKey: ['devcontainer-report', ws],
    queryFn: () => api.get<{ report: ReportItem[]; file: string }>(`/api/workspaces/${ws}/devcontainer?format=report`),
    enabled,
    retry: false,
  })
