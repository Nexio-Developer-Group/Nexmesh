import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'

// --- agent conversations and settings (PRD F17) ---

export type AgentLevel = 'read' | 'propose' | 'execute'
export interface AgentSettings { level: AgentLevel; autoAccept: boolean; configId?: string }

export type CallStatus = 'ok' | 'error' | 'denied' | 'needs_approval' | 'rejected'
export interface AgentCall {
  id: string
  name: string
  args?: Record<string, unknown>
  result?: string
  status: CallStatus
  changeSetId?: string
  proposalId?: string
}
export interface AgentTurn { id: string; role: 'user' | 'assistant'; content?: string; calls?: AgentCall[]; at: string }
export type SessionStatus = 'idle' | 'running' | 'awaiting_approval' | 'error' | 'interrupted'
export interface AgentSession {
  id: string
  title: string
  status: SessionStatus
  error?: string
  turns: AgentTurn[]
  pending?: { callId: string; command: string; reason: string }
  inputTokens: number
  outputTokens: number
  updatedAt: string
}
export interface SessionSummary { id: string; title: string; status: SessionStatus; updatedAt: string; turns: number }

export const agentKeys = {
  settings: (ws: string) => ['agent-settings', ws] as const,
  context: (ws: string) => ['agent-context', ws] as const,
  sessions: (ws: string) => ['agent-sessions', ws] as const,
  session: (ws: string, id: string) => ['agent-session', ws, id] as const,
  proposals: (ws: string) => ['agent-proposals', ws] as const,
  changes: (ws: string) => ['changes', ws] as const,
  change: (ws: string, id: string) => ['change', ws, id] as const,
  checkpoints: (ws: string) => ['checkpoints', ws] as const,
  gates: (ws: string) => ['gates', ws] as const,
  gateRuns: (ws: string, setId = '') => ['gate-runs', ws, setId] as const,
  gateRun: (ws: string, id: string) => ['gate-run', ws, id] as const,
  rules: (ws: string) => ['rules', ws] as const,
}

const A = (ws: string) => `/api/workspaces/${ws}/agent`

export const useAgentSettings = (ws: string) =>
  useQuery({ queryKey: agentKeys.settings(ws), queryFn: () => api.get<AgentSettings>(`${A(ws)}/settings`) })

export function useSaveAgentSettings(ws: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (s: AgentSettings) => api.put<AgentSettings>(`${A(ws)}/settings`, s),
    onSuccess: (s) => qc.setQueryData(agentKeys.settings(ws), s),
  })
}

export interface AgentContext {
  context: { workspace: string; cards: unknown[]; lines: unknown[]; variables: { name: string; secret: boolean }[]; rules?: string[] }
  text: string
}
export const useAgentContext = (ws: string, enabled: boolean) =>
  useQuery({ queryKey: agentKeys.context(ws), queryFn: () => api.get<AgentContext>(`${A(ws)}/context`), enabled, staleTime: 0 })

export const useSessions = (ws: string) =>
  useQuery({ queryKey: agentKeys.sessions(ws), queryFn: async () => (await api.get<{ sessions: SessionSummary[] }>(`${A(ws)}/sessions`)).sessions })

/** Polls while the agent is working so progress and approval requests show up. */
export const useSession = (ws: string, id: string | null) =>
  useQuery({
    queryKey: agentKeys.session(ws, id ?? ''),
    queryFn: () => api.get<AgentSession>(`${A(ws)}/sessions/${id}`),
    enabled: !!id,
    refetchInterval: (q) => (q.state.data && (q.state.data.status === 'running' || q.state.data.status === 'awaiting_approval') ? 700 : false),
  })

export function useCreateSession(ws: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => api.post<AgentSession>(`${A(ws)}/sessions`, {}),
    onSuccess: (s) => {
      qc.setQueryData(agentKeys.session(ws, s.id), s)
      qc.invalidateQueries({ queryKey: agentKeys.sessions(ws) })
    },
  })
}

export function useDeleteSession(ws: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.del<void>(`${A(ws)}/sessions/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: agentKeys.sessions(ws) }),
  })
}

export function useSendMessage(ws: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { id: string; text: string }) => api.post<AgentSession>(`${A(ws)}/sessions/${v.id}/messages`, { text: v.text }),
    onSuccess: (s, v) => {
      qc.setQueryData(agentKeys.session(ws, v.id), s)
      qc.invalidateQueries({ queryKey: agentKeys.sessions(ws) })
    },
  })
}

export function useStopSession(ws: string, id: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => api.post<void>(`${A(ws)}/sessions/${id}/stop`),
    onSuccess: () => qc.invalidateQueries({ queryKey: agentKeys.session(ws, id) }),
  })
}

export function useApproveCommand(ws: string, id: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { callId: string; approve: boolean }) => api.post<void>(`${A(ws)}/sessions/${id}/approve`, v),
    onSuccess: () => qc.invalidateQueries({ queryKey: agentKeys.session(ws, id) }),
  })
}

// --- canvas proposals ---

export interface Proposal {
  id: string
  kind: 'card' | 'connection'
  reason: string
  status: 'pending' | 'accepted' | 'rejected' | 'failed'
  error?: string
  catalogId?: string
  cardName?: string
  fromName?: string
  toName?: string
  resultId?: string
}
export const useProposals = (ws: string) =>
  useQuery({ queryKey: agentKeys.proposals(ws), queryFn: async () => (await api.get<{ proposals: Proposal[] }>(`${A(ws)}/proposals`)).proposals })

export function useDecideProposal(ws: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { id: string; accept: boolean }) => api.post<Proposal>(`${A(ws)}/proposals/${v.id}/${v.accept ? 'accept' : 'reject'}`),
    onSuccess: () => {
      for (const k of ['agent-proposals', 'components', 'wires', 'architecture', 'variables']) qc.invalidateQueries({ queryKey: [k, ws] })
    },
  })
}

// --- change sets and checkpoints (PRD F18) ---

export interface DiffLine { kind: 'context' | 'add' | 'del'; text: string }
export interface Hunk { id: number; oldStart: number; oldLines: number; newStart: number; newLines: number; added: number; removed: number; lines: DiffLine[] }
export interface ChangeFile { path: string; op: 'create' | 'modify' | 'delete'; status: 'pending' | 'applied' | 'partial' | 'rejected'; added: number; removed: number; hunks: Hunk[] }
export interface GateVerdict { configured: boolean; ran: boolean; passed: boolean; failing: string[]; runId?: string }
export interface ChangeSet {
  id: string
  title: string
  summary?: string
  actor: 'user' | 'agent'
  status: 'proposed' | 'applied' | 'partially_applied' | 'rejected' | 'reverted' | 'superseded'
  files: ChangeFile[]
  rules: { id: string; title: string; scope: string; hash: string }[]
  warnings?: string[]
  impact: { components: { id: string; name: string }[]; variables: string[] }
  gate?: GateVerdict
  gateOverride?: { reason: string; by: string; at: string; failing: string[] }
  checkpointId?: string
  createdAt: string
}

export const useChangeSets = (ws: string) =>
  useQuery({ queryKey: agentKeys.changes(ws), queryFn: async () => (await api.get<{ changeSets: ChangeSet[] }>(`/api/workspaces/${ws}/changes`)).changeSets, refetchInterval: 5000 })

export const useChangeSet = (ws: string, id: string) =>
  useQuery({ queryKey: agentKeys.change(ws, id), queryFn: () => api.get<ChangeSet>(`/api/workspaces/${ws}/changes/${id}`) })

export interface Decision { all?: boolean; files?: { path: string; hunks?: number[] }[]; overrideGates?: string }

function invalidateChanges(qc: ReturnType<typeof useQueryClient>, ws: string) {
  for (const k of ['changes', 'change', 'checkpoints', 'files', 'file', 'gate-runs']) qc.invalidateQueries({ queryKey: [k, ws] })
  qc.invalidateQueries({ queryKey: ['architecture', ws] })
}

export function useAcceptChange(ws: string, id: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (d: Decision) => api.post<ChangeSet>(`/api/workspaces/${ws}/changes/${id}/accept`, d),
    onSuccess: () => invalidateChanges(qc, ws),
  })
}

export function useRejectChange(ws: string, id: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => api.post<ChangeSet>(`/api/workspaces/${ws}/changes/${id}/reject`),
    onSuccess: () => invalidateChanges(qc, ws),
  })
}

export interface Checkpoint { id: string; setId?: string; label: string; files: string[]; createdAt: string; revertedAt?: string }
export const useCheckpoints = (ws: string) =>
  useQuery({ queryKey: agentKeys.checkpoints(ws), queryFn: async () => (await api.get<{ checkpoints: Checkpoint[] }>(`/api/workspaces/${ws}/checkpoints`)).checkpoints })

export function useRevert(ws: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { id: string; force?: boolean }) => api.post<Checkpoint>(`/api/workspaces/${ws}/checkpoints/${v.id}/revert`, { force: !!v.force }),
    onSuccess: () => invalidateChanges(qc, ws),
  })
}

// --- quality gates (PRD F23) ---

export type GateKind = 'format' | 'lint' | 'typecheck' | 'test' | 'build' | 'secrets' | 'deps' | 'custom'
export interface Gate { id: string; name: string; kind: GateKind; command: string; policy: 'required' | 'advisory'; enabled: boolean; timeoutSec: number; detected?: string }
export interface GateConfig { gates: Gate[]; confirmed: boolean }
export interface GateResult { gateId: string; name: string; kind: string; policy: string; status: 'passed' | 'failed' | 'timeout' | 'error' | 'skipped'; exitCode: number; millis: number; log?: string; truncated?: boolean }
export interface GateRun {
  id: string
  setId?: string
  status: 'running' | 'done' | 'error' | 'cancelled' | 'interrupted'
  error?: string
  startedAt: string
  finishedAt?: string
  results: GateResult[]
  passed: boolean
  requiredFailed: string[]
}

export const useGates = (ws: string) => useQuery({ queryKey: agentKeys.gates(ws), queryFn: () => api.get<GateConfig>(`/api/workspaces/${ws}/gates`) })
export const useDetectGates = (ws: string) => useMutation({ mutationFn: async () => (await api.get<{ gates: Gate[] }>(`/api/workspaces/${ws}/gates/detect`)).gates })

export function useSaveGates(ws: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (gates: Gate[]) => api.put<GateConfig>(`/api/workspaces/${ws}/gates`, { gates }),
    onSuccess: (c) => {
      qc.setQueryData(agentKeys.gates(ws), c)
      qc.invalidateQueries({ queryKey: ['changes', ws] })
      qc.invalidateQueries({ queryKey: ['change', ws] })
    },
  })
}

export const useGateRuns = (ws: string, setId = '') =>
  useQuery({ queryKey: agentKeys.gateRuns(ws, setId), queryFn: async () => (await api.get<{ runs: GateRun[] }>(`/api/workspaces/${ws}/gates/runs${setId ? `?setId=${setId}` : ''}`)).runs })

export const useGateRun = (ws: string, id: string | null) =>
  useQuery({
    queryKey: agentKeys.gateRun(ws, id ?? ''),
    queryFn: () => api.get<GateRun>(`/api/workspaces/${ws}/gates/runs/${id}`),
    enabled: !!id,
    refetchInterval: (q) => (q.state.data?.status === 'running' ? 800 : false),
  })

export function useStartGates(ws: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { setId?: string; gateIds?: string[] }) => api.post<GateRun>(`/api/workspaces/${ws}/gates/runs`, v),
    onSuccess: (r) => {
      qc.setQueryData(agentKeys.gateRun(ws, r.id), r)
      qc.invalidateQueries({ queryKey: ['gate-runs', ws] })
    },
  })
}

export function useCancelGates(ws: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.post<void>(`/api/workspaces/${ws}/gates/runs/${id}/cancel`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['gate-run', ws] }),
  })
}

// --- rules (PRD F21) ---

export type RuleScope = 'global' | 'component' | 'path'
export interface Rule {
  id: string
  name: string
  title: string
  scope: RuleScope
  components?: string[]
  paths?: string[]
  protect?: string[]
  body: string
  hash: string
  readOnly?: boolean
  problem?: string
}
export const useRules = (ws: string) =>
  useQuery({ queryKey: agentKeys.rules(ws), queryFn: () => api.get<{ rules: Rule[]; directory: string }>(`/api/workspaces/${ws}/rules`) })

export interface RuleInput { name?: string; title: string; scope: RuleScope; components: string[]; paths: string[]; protect: string[]; body: string }
export function useSaveRule(ws: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (r: RuleInput) => api.put<Rule>(`/api/workspaces/${ws}/rules`, r),
    onSuccess: () => { qc.invalidateQueries({ queryKey: agentKeys.rules(ws) }); qc.invalidateQueries({ queryKey: ['files', ws] }) },
  })
}
export function useDeleteRule(ws: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (name: string) => api.del<void>(`/api/workspaces/${ws}/rules/${name}`),
    onSuccess: () => { qc.invalidateQueries({ queryKey: agentKeys.rules(ws) }); qc.invalidateQueries({ queryKey: ['files', ws] }) },
  })
}

// --- per-card assistance (PRD F19) ---

export interface AssistPreview { id: string; kind: 'explain' | 'diagnose'; cardName: string; text: string; sections: { label: string; chars: number }[]; redactions: number; expiresAt: string }
export const useAssistPreview = (ws: string, cid: string) =>
  useMutation({ mutationFn: (kind: 'explain' | 'diagnose') => api.post<AssistPreview>(`/api/workspaces/${ws}/components/${cid}/assist/preview`, { kind }) })
export function useAssistSend(ws: string, cid: string) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (previewId: string) => api.post<AgentSession>(`/api/workspaces/${ws}/components/${cid}/assist`, { previewId }),
    onSuccess: (s) => {
      qc.setQueryData(agentKeys.session(ws, s.id), s)
      qc.invalidateQueries({ queryKey: agentKeys.sessions(ws) })
    },
  })
}
