import { useMutation, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import { keys } from '@/api/hooks'
import type { SettingsPreview, Workspace } from '@/api/types'

export interface SettingsBody {
  name?: string; description?: string; cpu?: number; ramGb?: number; storageGb?: number; gpu?: boolean; idleStopMinutes?: number; confirmRestart?: boolean
}

export const usePreviewSettings = (id: string) =>
  useMutation({ mutationFn: (b: SettingsBody) => api.post<SettingsPreview>(`/api/workspaces/${id}/settings`, b) })

export function useApplySettings(id: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (b: SettingsBody) => api.patch<Workspace>(`/api/workspaces/${id}/settings`, b),
    onSuccess: (w) => {
      qc.setQueryData(keys.workspace(id), w)
      qc.invalidateQueries({ queryKey: keys.workspaces })
    },
  })
}
