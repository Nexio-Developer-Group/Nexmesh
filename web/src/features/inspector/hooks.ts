import { useMutation, useQuery } from '@tanstack/react-query'
import { api } from '@/api/client'

export interface OpArg { name: string; label: string; required?: boolean; kind?: 'text' | 'code' | 'number'; hint?: string }
export interface Op { name: string; label: string; args: OpArg[] | null }
export interface InspectorCaps { available: boolean; reason?: string; readOnly: boolean; production?: boolean; ops: Op[] }
export interface InspectResult {
  columns: string[]
  rows: unknown[][]
  truncated?: boolean
  note?: string
  elapsedMs: number
  next?: string
  production?: boolean
}

const base = (ws: string, cid: string) => `/api/workspaces/${ws}/components/${cid}/inspect`

export const useInspectorCaps = (ws: string, cid: string, enabled = true) =>
  useQuery({ queryKey: ['inspector', ws, cid], queryFn: () => api.get<InspectorCaps>(base(ws, cid)), enabled })

export const useInspect = (ws: string, cid: string) =>
  useMutation({ mutationFn: (v: { op: string; args: Record<string, unknown> }) => api.post<InspectResult>(base(ws, cid), v) })

/** Where clicking the first cell of a result leads next, per technology. */
export const DRILL: Record<string, Record<string, { op: string; arg: string }>> = {
  mysql: { databases: { op: 'tables', arg: 'database' }, tables: { op: 'schema', arg: 'table' } },
  mongodb: { databases: { op: 'collections', arg: 'database' }, collections: { op: 'find', arg: 'collection' } },
  redis: { keys: { op: 'get', arg: 'key' } },
  kafka: { topics: { op: 'partitions', arg: 'topic' }, partitions: { op: 'sample', arg: 'topic' } },
}
