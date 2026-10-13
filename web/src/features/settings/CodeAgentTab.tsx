import clsx from 'clsx'
import { useEffect, useState } from 'react'
import { ApiError } from '@/api/client'
import type { AgentConfig, AgentModel, AgentProvider, AgentTestResult } from '@/api/types'
import { Button } from '@/components/ui/Button'
import { Field } from '@/components/ui/Field'
import { Modal } from '@/components/ui/Modal'
import { relativeTime } from '@/lib/format'
import { AgentConfigFields } from '@/features/agent/AgentConfigFields'
import { emptyForm, fromConfig, useAgentForm } from '@/features/agent/form'
import { useAgentConfigs, useAgentMutations, useAgentModels, useProviders, useTestAgent } from '@/features/agent/hooks'

const PROTOCOL_LABEL: Record<string, string> = { openai: 'OpenAI-compatible', anthropic: 'Anthropic-compatible', ollama: 'Ollama' }

export function CodeAgentTab({ workspaceId }: { workspaceId: string }) {
  const { data: providers = [] } = useProviders()
  const { data: configs = [], isLoading } = useAgentConfigs(workspaceId)
  const m = useAgentMutations(workspaceId)
  const [editing, setEditing] = useState<AgentConfig | 'new' | null>(null)
  const [keyFor, setKeyFor] = useState<AgentConfig | null>(null)
  const [deleting, setDeleting] = useState<AgentConfig | null>(null)
  const providerName = (id: string) => providers.find((p) => p.id === id)?.name ?? id

  return (
    <section aria-label="Code agent settings" className="max-w-5xl space-y-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h2 className="text-2xl font-extrabold tracking-tight">Code Agent</h2>
          <p className="mt-1 text-muted">Choose which model powers this workspace's code agent. Bring your own keys.</p>
        </div>
        <Button variant="primary" onClick={() => setEditing('new')}>Add configuration</Button>
      </div>
      <p className="flex gap-2 rounded-xl border border-info-fg/30 bg-info-bg p-3.5 text-[13px] text-info-fg">
        <span aria-hidden className="font-mono font-bold">i</span>
        Prompts and project context are sent to the selected provider. Local or self-hosted models keep that traffic in your infrastructure. Usage and rate limits belong to your provider account.
      </p>

      <div className="overflow-hidden rounded-card border border-line bg-panel">
        <table className="w-full text-left text-sm">
          <thead className="border-b border-line font-mono text-[11px] uppercase tracking-wider text-faint">
            <tr><th className="px-4 py-3 font-semibold">Name</th><th className="px-4 py-3 font-semibold">Provider</th><th className="px-4 py-3 font-semibold">Model</th><th className="px-4 py-3 font-semibold">Status</th><th className="px-4 py-3"><span className="sr-only">Actions</span></th></tr>
          </thead>
          <tbody>
            {isLoading && <tr><td colSpan={5} className="px-4 py-6 text-muted">Loading…</td></tr>}
            {!isLoading && configs.length === 0 && (
              <tr><td colSpan={5} className="px-4 py-10 text-center text-muted">No code agent configured yet. Add a configuration to choose a provider and model.</td></tr>
            )}
            {configs.map((c) => {
              const ok = c.lastTest?.passed
              const isEditing = typeof editing === 'object' && editing?.id === c.id
              return (
                <tr key={c.id} className={clsx('border-b border-line last:border-0', isEditing && 'bg-raised')}>
                  <td className="px-4 py-3 font-semibold">{c.name}{c.isDefault && <span className="ml-2 rounded bg-info-bg px-1.5 py-0.5 text-[10px] font-semibold text-info-fg">Default</span>}</td>
                  <td className="px-4 py-3">{providerName(c.provider)}{c.provider === 'custom' && c.protocol ? ` · ${PROTOCOL_LABEL[c.protocol] ?? c.protocol}` : ''}</td>
                  <td className="px-4 py-3 font-mono text-[12.5px]">{c.modelId}</td>
                  <td className="px-4 py-3">
                    {c.lastTest ? (
                      <span className={clsx('inline-flex items-center gap-1.5 text-[13px]', ok ? 'text-ok-fg' : 'text-err-fg')}><span aria-hidden>{ok ? '●' : '✕'}</span>{ok ? 'Passed' : 'Test failed'} <span className="text-faint">· {relativeTime(c.lastTest.at)}</span></span>
                    ) : <span className="text-faint">Not tested</span>}
                  </td>
                  <td className="px-4 py-3">
                    <div className="flex justify-end gap-3 whitespace-nowrap text-[13px]">
                      {isEditing ? <span className="text-muted">Editing</span> : (
                        <>
                          <button className="font-semibold text-info-fg hover:underline" onClick={() => setEditing(c)}>Edit</button>
                          <button className="text-info-fg hover:underline" disabled={m.retest.isPending} onClick={() => m.retest.mutate(c.id)}>Re-test</button>
                          {!c.isDefault && <button className="text-info-fg hover:underline" onClick={() => m.setDefault.mutate(c.id)}>Set as default</button>}
                          <button className="text-info-fg hover:underline" onClick={() => setKeyFor(c)}>Replace API key</button>
                          <button className="text-err-fg hover:underline" onClick={() => setDeleting(c)}>Delete</button>
                        </>
                      )}
                    </div>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
      {m.retest.error && <p role="alert" className="text-sm text-err-fg">{m.retest.error.message}</p>}

      {editing && (
        <ConfigEditor key={typeof editing === 'object' ? editing.id : 'new'} workspaceId={workspaceId} providers={providers} existing={typeof editing === 'object' ? editing : undefined}
          onClose={() => setEditing(null)} />
      )}

      {keyFor && <ReplaceKeyDialog workspaceId={workspaceId} config={keyFor} onClose={() => setKeyFor(null)} />}
      {deleting && (
        <Modal title={`Delete ${deleting.name}?`} onClose={() => setDeleting(null)}
          footer={<><Button onClick={() => setDeleting(null)}>Cancel</Button><Button variant="danger" loading={m.remove.isPending} onClick={() => m.remove.mutate(deleting.id, { onSuccess: () => setDeleting(null) })}>Delete configuration</Button></>}>
          <p>The stored credentials are deleted from Nexmesh. This does not revoke the key at the provider; revoke it there if it is no longer needed.</p>
          {m.remove.error && <p role="alert" className="mt-3 text-err-fg">{m.remove.error.message}</p>}
        </Modal>
      )}
    </section>
  )
}

function ConfigEditor({ workspaceId, providers, existing, onClose }: { workspaceId: string; providers: AgentProvider[]; existing?: AgentConfig; onClose: () => void }) {
  const form = useAgentForm(existing ? fromConfig(existing) : emptyForm(), providers)
  const m = useAgentMutations(workspaceId)
  const testNew = useTestAgent()
  const loadModels = useAgentModels()
  const [result, setResult] = useState<AgentTestResult | null>(existing?.lastTest ? (existing.lastTest as AgentTestResult) : null)
  const [models, setModels] = useState<AgentModel[]>([])
  const [passedKey, setPassedKey] = useState<string | null>(null)
  const { key, input, value } = form
  const testing = testNew.isPending || m.testEdit.isPending
  const canSave = passedKey === key && !!value.provider
  const saving = m.create.isPending || m.update.isPending
  const saveErr = (m.create.error ?? m.update.error) as ApiError | null

  useEffect(() => { setPassedKey((k) => (k === key ? k : null)) }, [key])

  const run = () => {
    const done = (r: AgentTestResult) => { setResult(r); if (r.models?.length) setModels(r.models); setPassedKey(r.passed ? key : null) }
    if (existing) m.testEdit.mutate({ id: existing.id, body: input }, { onSuccess: done })
    else testNew.mutate(input, { onSuccess: done })
  }
  const save = () => {
    if (existing) m.update.mutate({ id: existing.id, body: input }, { onSuccess: onClose })
    else m.create.mutate(input, { onSuccess: onClose })
  }

  return (
    <div className="rounded-card border border-line bg-panel p-6" role="region" aria-label={existing ? `Edit ${existing.name}` : 'Add configuration'}>
      <h3 className="mb-4 text-base font-bold">{existing ? `Edit · ${existing.name}` : 'Add configuration'} {value.provider && <span className="ml-2 text-xs font-normal text-muted">{providers.find((p) => p.id === value.provider)?.hosted ? 'Hosted provider' : 'Custom or local model'}</span>}</h3>
      <AgentConfigFields providers={providers} form={form} existing={existing} lockProvider={!!existing} models={models}
        loadingModels={loadModels.isPending} modelsError={loadModels.error?.message}
        onLoadModels={() => loadModels.mutate(input, { onSuccess: (r) => setModels(r.models) })}
        onTest={run} testing={testing} result={result} testError={(testNew.error ?? m.testEdit.error)?.message}
        hint={canSave ? 'Test passed for these exact values.' : 'Save is enabled after a passing test.'}
        actions={<><Button variant="primary" disabled={!canSave} loading={saving} onClick={save}>Save</Button><Button variant="ghost" onClick={onClose}>Cancel</Button></>} />
      {saveErr && <p role="alert" className="mt-3 rounded-lg bg-err-bg p-3 text-sm text-err-fg">{saveErr.message}</p>}
    </div>
  )
}

function ReplaceKeyDialog({ workspaceId, config, onClose }: { workspaceId: string; config: AgentConfig; onClose: () => void }) {
  const m = useAgentMutations(workspaceId)
  const [key, setKey] = useState('')
  return (
    <Modal title={`Replace API key · ${config.name}`} onClose={onClose}
      footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" disabled={!key} loading={m.replaceKey.isPending} onClick={() => m.replaceKey.mutate({ id: config.id, apiKey: key }, { onSuccess: onClose })}>Test and replace</Button></>}>
      <p>The new key is tested first. The stored key is replaced only if the test passes. Keys are write-only: you can replace one but never view it.</p>
      <Field className="mt-4" label="New API key" type="password" autoComplete="new-password" value={key} onChange={(e) => setKey(e.target.value)} />
      {m.replaceKey.error && <p role="alert" className="mt-3 rounded-lg bg-err-bg p-2.5 text-err-fg">{m.replaceKey.error.message}</p>}
    </Modal>
  )
}
