import { useInfiniteQuery } from '@tanstack/react-query'
import { api } from '@/api/client'

export interface AuditEvent {
  id: string; actorType: 'user' | 'agent' | 'system'; actorId?: string; action: string; target?: string
  detail?: Record<string, unknown>; at: string
}
export interface AuditFilters { actor: string; action: string; q: string; from: string; to: string }
export const emptyFilters: AuditFilters = { actor: '', action: '', q: '', from: '', to: '' }

export function auditParams(f: AuditFilters, extra: Record<string, string> = {}) {
  const p = new URLSearchParams()
  for (const [k, v] of Object.entries({ ...f, ...extra })) if (v) p.set(k, v)
  return p.toString()
}

export const useAudit = (ws: string, f: AuditFilters) =>
  useInfiniteQuery({
    queryKey: ['audit', ws, f],
    initialPageParam: '',
    queryFn: ({ pageParam }) => api.get<{ events: AuditEvent[]; next?: string; truncated?: boolean }>(`/api/workspaces/${ws}/audit?${auditParams(f, { after: pageParam, limit: '50' })}`),
    getNextPageParam: (last) => last.next || undefined,
    retry: false,
  })
