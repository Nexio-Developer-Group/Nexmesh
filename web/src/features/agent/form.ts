import { useMemo, useState } from 'react'
import type { AgentConfig, AgentInput, AgentProvider } from '@/api/types'

export interface AgentFormValue {
  name: string
  provider: string
  modelId: string
  apiKey: string
  endpoint: string // custom endpoint, or base URL override for hosted providers
  protocol: string
  organization: string
  project: string
  authStyle: string
  headerName: string
  customHeaders: { name: string; value: string }[]
  tlsInsecure: boolean
  caCertificate: string
  maxOutputTokens: number | ''
  temperature: number | ''
  contextWindow: number | ''
  timeoutSeconds: number | ''
  retries: number | ''
  capabilities: { streaming: boolean; tools: boolean; vision: boolean }
}

export const emptyForm = (): AgentFormValue => ({
  name: '', provider: '', modelId: '', apiKey: '', endpoint: '', protocol: 'openai', organization: '', project: '', authStyle: 'bearer', headerName: '',
  customHeaders: [], tlsInsecure: false, caCertificate: '', maxOutputTokens: '', temperature: '', contextWindow: '', timeoutSeconds: '', retries: '',
  capabilities: { streaming: true, tools: false, vision: false },
})

const numOrUndef = (v: number | '') => (v === '' || Number.isNaN(v) ? undefined : v)

/** Converts form state to the API body. Empty optional fields are omitted. */
export function toInput(v: AgentFormValue, providers: AgentProvider[]): AgentInput {
  const p = providers.find((x) => x.id === v.provider)
  const headers = Object.fromEntries(v.customHeaders.filter((h) => h.name.trim() && h.value).map((h) => [h.name.trim(), h.value]))
  const custom = p ? !p.hosted : false
  return {
    name: v.name.trim() || defaultName(v, p), provider: v.provider, modelId: v.modelId.trim(),
    endpoint: v.endpoint.trim() || undefined, protocol: custom ? v.protocol : undefined, apiKey: v.apiKey || undefined,
    organization: v.organization || undefined, project: v.project || undefined,
    authStyle: custom && v.apiKey ? v.authStyle : undefined, headerName: custom && v.authStyle === 'header' ? v.headerName : undefined,
    customHeaders: Object.keys(headers).length ? headers : undefined, tlsInsecure: v.tlsInsecure || undefined, caCertificate: v.caCertificate || undefined,
    maxOutputTokens: numOrUndef(v.maxOutputTokens), temperature: numOrUndef(v.temperature), contextWindow: numOrUndef(v.contextWindow),
    timeoutSeconds: numOrUndef(v.timeoutSeconds), retries: numOrUndef(v.retries), capabilities: custom ? v.capabilities : undefined,
  }
}

function defaultName(v: AgentFormValue, p?: AgentProvider) {
  return [p?.name ?? 'Model', v.modelId.trim()].filter(Boolean).join(' · ').slice(0, 64)
}

/** Restores form state from a saved configuration (the key is never returned). */
export function fromConfig(c: AgentConfig): AgentFormValue {
  const p = c.params
  const caps = (p.capabilities as AgentFormValue['capabilities'] | undefined) ?? emptyForm().capabilities
  const n = (k: string): number | '' => (typeof p[k] === 'number' && p[k] !== 0 ? (p[k] as number) : '')
  return {
    ...emptyForm(), name: c.name, provider: c.provider, modelId: c.modelId, endpoint: c.endpoint ?? '', protocol: c.protocol || 'openai',
    organization: String(p.organization ?? ''), project: String(p.project ?? ''), authStyle: String(p.authStyle || 'bearer'), headerName: String(p.headerName ?? ''),
    customHeaders: ((p.customHeaderNames as string[] | undefined) ?? []).map((name) => ({ name, value: '' })),
    tlsInsecure: p.tlsInsecure === true, caCertificate: String(p.caCertificate ?? ''), capabilities: caps,
    maxOutputTokens: n('maxOutputTokens'), contextWindow: n('contextWindow'), timeoutSeconds: n('timeoutSeconds'),
    temperature: typeof p.temperature === 'number' ? p.temperature : '', retries: typeof p.retries === 'number' ? p.retries : '',
  }
}

/** True when the address is loopback or on a private network (PRD 12.5 warning). */
export function isPrivateAddress(endpoint: string): string | null {
  let host = ''
  try { host = new URL(endpoint).hostname } catch { return null }
  host = host.replace(/^\[|\]$/g, '').toLowerCase()
  if (host === 'localhost' || host.endsWith('.localhost') || host === '::1') return host
  const m = host.match(/^(\d+)\.(\d+)\.(\d+)\.(\d+)$/)
  if (m) {
    const [a, b] = [Number(m[1]), Number(m[2])]
    if (a === 127 || a === 10 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168) || (a === 169 && b === 254) || (a === 100 && b >= 64 && b <= 127)) return host
  }
  if (/^(fc|fd|fe80)/.test(host)) return host
  return null
}

/** Has the user started configuring an agent? Used by Advanced Setup (PRD 6.4). */
export function isStarted(v: AgentFormValue): boolean {
  return !!(v.provider || v.apiKey || v.modelId || v.endpoint)
}

export function useAgentForm(initial: AgentFormValue, providers: AgentProvider[]) {
  const [value, setValue] = useState<AgentFormValue>(initial)
  const input = useMemo(() => toInput(value, providers), [value, providers])
  const key = JSON.stringify(input)
  const set = <K extends keyof AgentFormValue>(k: K, v: AgentFormValue[K]) => setValue((p) => ({ ...p, [k]: v }))
  return { value, setValue, set, input, key }
}
