import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import type { CanvasComponent } from '@/api/types'
import { compKeys } from '@/features/canvas/hooks'

export interface TemplateComponent { id: string; x: number; y: number; suggestedMode?: 'install' | 'connect'; version?: string }
export interface TemplateEntry { id: string; name: string; description: string; personal: boolean; components: TemplateComponent[] }
export interface Gallery { curated: TemplateEntry[]; personal: TemplateEntry[] }

export const useTemplates = () => useQuery({ queryKey: ['templates'], queryFn: () => api.get<Gallery>('/api/templates'), staleTime: 30_000 })

export function useTemplateActions(ws: string) {
  const qc = useQueryClient()
  return {
    apply: useMutation({
      mutationFn: (id: string) => api.post<{ added: CanvasComponent[]; skipped: string[] }>(`/api/workspaces/${ws}/templates/${id}/apply`),
      onSuccess: () => qc.invalidateQueries({ queryKey: compKeys.list(ws) }),
    }),
    save: useMutation({
      mutationFn: (v: { name: string; description: string }) => api.post<TemplateEntry>(`/api/workspaces/${ws}/templates`, v),
      onSuccess: () => qc.invalidateQueries({ queryKey: ['templates'] }),
    }),
    remove: useMutation({
      mutationFn: (id: string) => api.del<void>(`/api/templates/${id}`),
      onSuccess: () => qc.invalidateQueries({ queryKey: ['templates'] }),
    }),
  }
}
