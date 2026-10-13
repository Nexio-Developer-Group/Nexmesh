import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import type { AgentConfig, AgentInput, AgentModel, AgentProvider, AgentTestResult } from '@/api/types'

export const agentKeys = { configs: (ws: string) => ['agent-configs', ws] as const, providers: ['agent-providers'] as const }

export const useProviders = () =>
  useQuery({ queryKey: agentKeys.providers, queryFn: async () => (await api.get<{ providers: AgentProvider[] }>('/api/agent/providers')).providers, staleTime: Infinity })

export const useAgentConfigs = (ws: string) =>
  useQuery({ queryKey: agentKeys.configs(ws), queryFn: async () => (await api.get<{ configs: AgentConfig[] }>(`/api/workspaces/${ws}/agent-configs`)).configs })

export const useTestAgent = () => useMutation({ mutationFn: (b: AgentInput) => api.post<AgentTestResult>('/api/agent/test', b) })
export const useAgentModels = () => useMutation({ mutationFn: (b: AgentInput) => api.post<{ models: AgentModel[] }>('/api/agent/models', b) })

export function useAgentMutations(ws: string) {
  const qc = useQueryClient()
  const refresh = () => qc.invalidateQueries({ queryKey: agentKeys.configs(ws) })
  return {
    create: useMutation({ mutationFn: (b: AgentInput) => api.post<AgentConfig>(`/api/workspaces/${ws}/agent-configs`, b), onSuccess: refresh }),
    update: useMutation({ mutationFn: (v: { id: string; body: AgentInput }) => api.put<AgentConfig>(`/api/workspaces/${ws}/agent-configs/${v.id}`, v.body), onSuccess: refresh }),
    testEdit: useMutation({ mutationFn: (v: { id: string; body: AgentInput }) => api.post<AgentTestResult>(`/api/workspaces/${ws}/agent-configs/${v.id}/test`, v.body) }),
    remove: useMutation({ mutationFn: (id: string) => api.del<void>(`/api/workspaces/${ws}/agent-configs/${id}`), onSuccess: refresh }),
    setDefault: useMutation({ mutationFn: (id: string) => api.post<void>(`/api/workspaces/${ws}/agent-configs/${id}/default`), onSuccess: refresh }),
    retest: useMutation({ mutationFn: (id: string) => api.post<AgentTestResult>(`/api/workspaces/${ws}/agent-configs/${id}/retest`), onSuccess: refresh }),
    replaceKey: useMutation({ mutationFn: (v: { id: string; apiKey: string }) => api.put<AgentConfig>(`/api/workspaces/${ws}/agent-configs/${v.id}/key`, { apiKey: v.apiKey }), onSuccess: refresh }),
  }
}
