import clsx from 'clsx'
import { useId, useState } from 'react'
import type { AgentConfig, AgentModel, AgentProvider, AgentTestResult } from '@/api/types'
import { Button } from '@/components/ui/Button'
import { Field } from '@/components/ui/Field'
import { TestResultList } from '@/features/connect/TestResultList'
import { isPrivateAddress, type AgentFormValue, type useAgentForm } from './form'

export const AGENT_STAGES = [
  { key: 'input_validation', label: 'Required fields' },
  { key: 'network', label: 'Endpoint reachable' },
  { key: 'authentication', label: 'Key accepted' },
  { key: 'model', label: 'Model available' },
  { key: 'request', label: 'Test request' },
]

const PROTOCOLS: [string, string][] = [['openai', 'OpenAI-compatible'], ['anthropic', 'Anthropic-compatible'], ['ollama', 'Ollama-native']]

interface Props {
  providers: AgentProvider[]
  form: ReturnType<typeof useAgentForm>
  existing?: AgentConfig
  lockProvider?: boolean
  models: AgentModel[]
  onLoadModels: () => void
  loadingModels?: boolean
  modelsError?: string
  onTest: () => void
  testing?: boolean
  result?: AgentTestResult | null
  testError?: string
  /** Shown beside the test button, e.g. "Save is enabled after a passing test." */
  hint?: string
  /** Hide the name field (the Create screen derives a default name). */
  hideName?: boolean
  actions?: React.ReactNode
}

function Seg({ label, on, onClick }: { label: string; on: boolean; onClick: () => void }) {
  return (
    <button type="button" role="switch" aria-checked={on} onClick={onClick}
      className={clsx('rounded-lg border px-3 py-1.5 text-[13px] font-medium', on ? 'border-blue bg-blue/10 text-fg' : 'border-line-strong text-muted hover:text-fg')}>
      <span aria-hidden>{on ? '✓ ' : ''}</span>{label}
    </button>
  )
}

function NumField({ label, unit, value, onChange, help }: { label: string; unit?: string; value: number | ''; onChange: (v: number | '') => void; help?: string }) {
  return <Field label={label} mono type="number" suffix={unit} hint={help} value={value} onChange={(e) => onChange(e.target.value === '' ? '' : Number(e.target.value))} />
}

/** Provider, model, credentials and settings for one code agent configuration (PRD 12.3, 12.4). */
export function AgentConfigFields({ providers, form, existing, lockProvider, models, onLoadModels, loadingModels, modelsError, onTest, testing, result, testError, hint, hideName, actions }: Props) {
  const { value: v, set } = form
  const provider = providers.find((p) => p.id === v.provider)
  const custom = provider && !provider.hosted
  const [manualModel, setManualModel] = useState(false)
  const modelId = useId()
  const priv = custom ? isPrivateAddress(v.endpoint) : null
  const hasStoredKey = !!existing?.hasKey

  const pickProvider = (p: AgentProvider) => {
    set('provider', p.id)
    set('modelId', p.defaultModel ?? '')
    set('endpoint', '')
    set('apiKey', '')
    setManualModel(false)
  }

  return (
    <div className="space-y-5">
      <div role="radiogroup" aria-label="Provider" className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        {providers.map((p) => {
          const sel = v.provider === p.id
          return (
            <button key={p.id} type="button" role="radio" aria-checked={sel} disabled={lockProvider && !sel} onClick={() => pickProvider(p)}
              className={clsx('rounded-xl border px-3.5 py-2.5 text-left transition-colors disabled:opacity-40', sel ? 'border-blue bg-blue/10' : 'border-line-strong hover:border-blue/60')}>
              <span className="block text-sm font-semibold">{p.name}</span>
              <span className="block text-xs text-muted">{p.tagline}</span>
            </button>
          )
        })}
      </div>

      {provider && (
        <>
          {!hideName && <Field label="Configuration name" value={v.name} onChange={(e) => set('name', e.target.value)} placeholder="Claude for backend work" maxLength={64} />}

          {custom && (
            <div className="space-y-1.5">
              <Field label="Endpoint base URL" mono value={v.endpoint} onChange={(e) => set('endpoint', e.target.value)} placeholder="https://models.internal.example.com/v1" autoComplete="off" spellCheck={false} />
              {priv && (
                <p role="note" className="flex gap-2 rounded-lg bg-warn-bg p-2.5 text-xs text-warn-fg">
                  <span aria-hidden>▲</span>
                  <span>Private address. It is reachable only if the control plane (and the workspace) can route to {priv}. A model running on your own machine is not reachable from a remote workspace unless you expose it through a reachable endpoint.</span>
                </p>
              )}
            </div>
          )}

          <div className="grid gap-4 sm:grid-cols-2">
            {custom && (
              <div>
                <label htmlFor={modelId + '-p'} className="mb-1.5 block text-[13px] font-semibold">API protocol</label>
                <select id={modelId + '-p'} value={v.protocol} onChange={(e) => set('protocol', e.target.value)} className="h-10 w-full rounded-[10px] border border-line-strong bg-field px-3 text-sm">
                  {PROTOCOLS.filter(([id]) => provider.protocols.includes(id)).map(([id, label]) => <option key={id} value={id}>{label}</option>)}
                </select>
              </div>
            )}

            <div>
              <div className="mb-1.5 flex items-baseline justify-between">
                <label htmlFor={modelId} className="text-[13px] font-semibold">{custom ? 'Model ID' : 'Model'}</label>
                {!custom && <button type="button" className="text-xs text-info-fg hover:underline" onClick={() => setManualModel((m) => !m)}>{manualModel || models.length === 0 ? (models.length ? 'Choose from the list' : '') : 'Enter model ID manually'}</button>}
              </div>
              {!custom && models.length > 0 && !manualModel ? (
                <select id={modelId} value={v.modelId} onChange={(e) => set('modelId', e.target.value)} className="h-10 w-full rounded-[10px] border border-line-strong bg-field px-3 font-mono text-[13px]">
                  {!models.some((m) => m.id === v.modelId) && v.modelId && <option value={v.modelId}>{v.modelId}</option>}
                  {models.map((m) => <option key={m.id} value={m.id}>{m.id}</option>)}
                </select>
              ) : (
                <input id={modelId} value={v.modelId} onChange={(e) => set('modelId', e.target.value)} placeholder={provider.defaultModel ?? 'model-id'} spellCheck={false} autoComplete="off"
                  className="h-10 w-full rounded-[10px] border border-line-strong bg-field px-3 font-mono text-[13px]" />
              )}
              <div className="mt-1.5 flex items-center gap-3 text-xs text-muted">
                <button type="button" className="text-info-fg hover:underline disabled:opacity-50" disabled={loadingModels || (!v.apiKey && !hasStoredKey && !custom)} onClick={onLoadModels}>{loadingModels ? 'Loading models…' : 'Load models'}</button>
                {modelsError && <span role="alert" className="text-err-fg">{modelsError}</span>}
              </div>
            </div>
          </div>

          <div className="grid gap-4 sm:grid-cols-2">
            <div className={custom ? '' : 'sm:col-span-2'}>
              <Field label={custom ? 'API key or token' : 'API key'} optional={!!custom} type="password" autoComplete="new-password" spellCheck={false}
                suffix="Write-only" value={v.apiKey} onChange={(e) => set('apiKey', e.target.value)}
                placeholder={hasStoredKey ? '•••••••• (stored)' : custom ? 'None: server needs no key' : ''}
                hint={hasStoredKey ? 'Stored. Leave empty to keep it, or type a new key to replace it.' : (provider.fields.find((f) => f.name === 'apiKey')?.help ?? 'Never shown again after saving. Not injected into project code.')} />
            </div>
            {custom && (
              <NumField label="Context window" unit="tokens" value={v.contextWindow} onChange={(x) => set('contextWindow', x)} help="Often cannot be detected from custom endpoints." />
            )}
          </div>

          {custom && (
            <fieldset>
              <legend className="mb-1.5 text-[13px] font-semibold">Declared capabilities</legend>
              <div className="flex flex-wrap gap-2">
                <Seg label="Streaming" on={v.capabilities.streaming} onClick={() => set('capabilities', { ...v.capabilities, streaming: !v.capabilities.streaming })} />
                <Seg label="Tool calling" on={v.capabilities.tools} onClick={() => set('capabilities', { ...v.capabilities, tools: !v.capabilities.tools })} />
                <Seg label="Image input" on={v.capabilities.vision} onClick={() => set('capabilities', { ...v.capabilities, vision: !v.capabilities.vision })} />
              </div>
              {!v.capabilities.tools && <p className="mt-1.5 text-xs text-muted">Without tool calling the agent runs in a restricted mode.</p>}
            </fieldset>
          )}

          <details className="rounded-[10px] border border-line-strong bg-field/50">
            <summary className="cursor-pointer list-none px-3 py-2.5 text-sm font-medium">Common settings and advanced options <span className="text-xs text-muted">max tokens, temperature, timeout, retries{custom ? ', authentication, TLS' : ', base URL'}</span></summary>
            <div className="grid gap-4 border-t border-line p-3 sm:grid-cols-2">
              <NumField label="Max output tokens" value={v.maxOutputTokens} onChange={(x) => set('maxOutputTokens', x)} help="Default 4096." />
              <NumField label="Temperature" value={v.temperature} onChange={(x) => set('temperature', x)} help="0 to 2, where the model supports it." />
              <NumField label="Timeout" unit="s" value={v.timeoutSeconds} onChange={(x) => set('timeoutSeconds', x)} help={custom ? 'Local models can be slow. Default 60 s.' : 'Default 30 s.'} />
              <NumField label="Retries" value={v.retries} onChange={(x) => set('retries', x)} help="Default 2." />
              {!custom && <Field className="sm:col-span-2" label="Base URL" optional mono value={v.endpoint} onChange={(e) => set('endpoint', e.target.value)} placeholder={provider.baseUrl} hint="For proxies or gateways." />}
              {provider.id === 'openai' && (<><Field label="Organization ID" optional mono value={v.organization} onChange={(e) => set('organization', e.target.value)} /><Field label="Project ID" optional mono value={v.project} onChange={(e) => set('project', e.target.value)} /></>)}
              {custom && (
                <>
                  <div>
                    <label htmlFor={modelId + '-a'} className="mb-1.5 block text-[13px] font-semibold">Authentication style</label>
                    <select id={modelId + '-a'} value={v.authStyle} onChange={(e) => set('authStyle', e.target.value)} className="h-10 w-full rounded-[10px] border border-line-strong bg-field px-3 text-sm">
                      <option value="bearer">Bearer token</option><option value="header">Custom header</option>
                    </select>
                  </div>
                  {v.authStyle === 'header' && <Field label="Header name" mono value={v.headerName} onChange={(e) => set('headerName', e.target.value)} placeholder="X-Api-Key" />}
                  <div className="sm:col-span-2">
                    <p className="mb-1.5 text-[13px] font-semibold">Custom headers <span className="font-normal text-faint">optional · values are stored as secrets</span></p>
                    {v.customHeaders.map((h, i) => (
                      <div key={i} className="mb-2 grid grid-cols-[1fr_1fr_auto] gap-2">
                        <input aria-label={`Header ${i + 1} name`} value={h.name} onChange={(e) => set('customHeaders', v.customHeaders.map((x, j) => (j === i ? { ...x, name: e.target.value } : x)))} placeholder="X-Tenant" className="h-9 rounded-lg border border-line-strong bg-field px-2 font-mono text-xs" />
                        <input aria-label={`Header ${i + 1} value`} type="password" autoComplete="new-password" value={h.value} onChange={(e) => set('customHeaders', v.customHeaders.map((x, j) => (j === i ? { ...x, value: e.target.value } : x)))} placeholder="value" className="h-9 rounded-lg border border-line-strong bg-field px-2 font-mono text-xs" />
                        <button type="button" aria-label={`Remove header ${i + 1}`} className="px-2 text-muted hover:text-err-fg" onClick={() => set('customHeaders', v.customHeaders.filter((_, j) => j !== i))}>×</button>
                      </div>
                    ))}
                    <button type="button" className="text-xs text-info-fg hover:underline" onClick={() => set('customHeaders', [...v.customHeaders, { name: '', value: '' }])}>Add header</button>
                  </div>
                  <div className="sm:col-span-2">
                    <label htmlFor={modelId + '-ca'} className="mb-1.5 block text-[13px] font-semibold">CA certificate <span className="font-normal text-faint">optional</span></label>
                    <textarea id={modelId + '-ca'} rows={3} value={v.caCertificate} onChange={(e) => set('caCertificate', e.target.value)} spellCheck={false} placeholder="-----BEGIN CERTIFICATE-----" className="w-full rounded-[10px] border border-line-strong bg-field p-3 font-mono text-[11px]" />
                  </div>
                  <label className="flex items-start gap-2.5 text-sm sm:col-span-2">
                    <input type="checkbox" role="switch" checked={v.tlsInsecure} onChange={(e) => set('tlsInsecure', e.target.checked)} className="mt-0.5 size-4 accent-blue" />
                    <span><span className="font-semibold">Skip certificate verification</span><span className="block text-xs text-muted">The connection is encrypted but the server's identity is not checked. Only for testing.</span></span>
                  </label>
                  {v.tlsInsecure && <p role="alert" className="rounded-lg bg-warn-bg p-2.5 text-xs text-warn-fg sm:col-span-2">Certificate verification is off for this model endpoint.</p>}
                </>
              )}
            </div>
          </details>

          <div className="flex flex-wrap items-center gap-3">
            <Button onClick={onTest} loading={testing} disabled={!v.modelId.trim()}>Test Configuration</Button>
            {result?.passed && !testing && (
              <span role="status" className="inline-flex items-center gap-2 text-xs">
                <span className="rounded-md bg-ok-bg px-2 py-1 font-semibold text-ok-fg">✓ Passed</span>
                <span className="font-mono text-muted">key accepted · model available · {result.latencyMs ?? 0} ms</span>
              </span>
            )}
            {hint && <span className="text-xs text-muted">{hint}</span>}
            {actions}
          </div>

          {testError && <p role="alert" className="rounded-lg bg-err-bg p-3 text-sm text-err-fg">{testError}</p>}
          {result && !result.passed && !testing && (
            <p role="alert" className="flex gap-2 rounded-lg border border-err-fg/40 bg-err-bg p-3 text-sm text-err-fg"><span aria-hidden>✕</span><span><strong>{AGENT_STAGES.find((s) => s.key === result.failedStage)?.label ?? 'Test'} failed.</strong> {result.message}</span></p>
          )}
          {(result || testing) && <TestResultList result={result} running={testing} stages={AGENT_STAGES} title="Configuration test" />}
          {provider.notice && <p className="rounded-lg bg-info-bg p-3 text-[13px] text-info-fg">{provider.notice}</p>}
          {provider.comingSoon?.map((c) => <p key={c} className="text-xs text-muted">Coming soon: {c}</p>)}
        </>
      )}
    </div>
  )
}

export type { AgentFormValue }
