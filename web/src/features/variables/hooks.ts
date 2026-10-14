import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'

export interface VarRow {
  name: string
  componentId?: string
  secret: boolean
  value?: string
  description?: string
  overridesGenerated?: boolean
  updatedAt: string
}
export interface GeneratedVar { name: string; componentId: string; componentName: string; overridden?: boolean }
export interface VarListing { variables: VarRow[]; generated: GeneratedVar[] }
export interface ImportResult {
  created: string[]; updated: string[]; skipped: string[]; secrets: string[]
  errors: { line: number; message: string }[]; dryRun: boolean
}

export const varKeys = { list: (ws: string) => ['variables', ws] as const }

export const useVariables = (ws: string) =>
  useQuery({ queryKey: varKeys.list(ws), queryFn: () => api.get<VarListing>(`/api/workspaces/${ws}/variables`) })

export function useSaveVariable(ws: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { name: string; componentId?: string; secret: boolean; value?: string; description?: string }) => {
      const { name, ...body } = v
      return api.put<VarRow>(`/api/workspaces/${ws}/variables/${encodeURIComponent(name)}`, { componentId: v.componentId ?? '', ...body })
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: varKeys.list(ws) }),
  })
}

export function useDeleteVariable(ws: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { name: string; componentId?: string }) =>
      api.del<void>(`/api/workspaces/${ws}/variables/${encodeURIComponent(v.name)}?componentId=${encodeURIComponent(v.componentId ?? '')}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: varKeys.list(ws) }),
  })
}

export function useImportVariables(ws: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (b: { content: string; secrets: 'auto' | 'all' | 'none'; overwrite: boolean; dryRun: boolean }) =>
      api.post<ImportResult>(`/api/workspaces/${ws}/variables/import`, b),
    onSuccess: (r) => { if (!r.dryRun) qc.invalidateQueries({ queryKey: varKeys.list(ws) }) },
  })
}
