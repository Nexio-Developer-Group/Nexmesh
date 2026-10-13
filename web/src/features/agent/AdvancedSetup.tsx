import { useEffect, useState } from 'react'
import type { AgentInput, AgentModel, AgentTestResult } from '@/api/types'
import { Button } from '@/components/ui/Button'
import { AgentConfigFields } from './AgentConfigFields'
import { emptyForm, isStarted, useAgentForm } from './form'
import { useAgentModels, useProviders, useTestAgent } from './hooks'

export interface AdvancedState {
  /** The user typed or chose something in this section. */
  started: boolean
  /** Test Configuration passed for exactly the current values. */
  tested: boolean
  /** The configuration to save with the workspace; set only when started and tested. */
  input: AgentInput | null
  providerName?: string
}

/**
 * Collapsible Advanced Setup on the Create Code Space screen (PRD 6.4). It is
 * optional: an untouched section never blocks creation, but a section the user
 * started and left untested does.
 */
export function AdvancedSetup({ onChange }: { onChange: (s: AdvancedState) => void }) {
  const { data: providers = [] } = useProviders()
  const form = useAgentForm(emptyForm(), providers)
  const test = useTestAgent()
  const loadModels = useAgentModels()
  const [open, setOpen] = useState(false)
  const [result, setResult] = useState<AgentTestResult | null>(null)
  const [models, setModels] = useState<AgentModel[]>([])
  const [passedKey, setPassedKey] = useState<string | null>(null)
  const { key, input, value } = form
  const started = isStarted(value)
  const tested = passedKey === key
  const providerName = providers.find((p) => p.id === value.provider)?.name

  useEffect(() => { onChange({ started, tested, input: started && tested ? input : null, providerName }) }, [started, tested, key]) // eslint-disable-line react-hooks/exhaustive-deps

  const clear = () => { form.setValue(emptyForm()); setResult(null); setModels([]); setPassedKey(null) }

  return (
    <section className="rounded-card border border-line bg-panel" aria-label="Advanced Setup">
      <div className="flex items-center justify-between px-6 py-4">
        <h2 className="flex items-baseline gap-3 text-base font-bold">
          <span className="font-mono text-[11px] text-link">04</span>Advanced Setup
          <span className="rounded-md bg-neutral-bg px-2 py-0.5 text-xs font-normal text-muted">Optional</span>
        </h2>
        <button type="button" aria-expanded={open} aria-controls="advanced-body" className="text-sm font-semibold text-link hover:underline" onClick={() => setOpen((o) => !o)}>
          {open ? 'Collapse' : 'Expand'}
        </button>
      </div>
      {open && (
        <div id="advanced-body" className="space-y-4 border-t border-line px-6 py-5">
          <div>
            <h3 className="text-[15px] font-bold">Code Agent</h3>
            <p className="text-sm text-muted">Choose the model that powers this workspace's code agent. You can skip this and set it up later.</p>
          </div>
          <AgentConfigFields providers={providers} form={form} hideName models={models}
            loadingModels={loadModels.isPending} modelsError={loadModels.error?.message}
            onLoadModels={() => loadModels.mutate(input, { onSuccess: (r) => setModels(r.models) })}
            onTest={() => test.mutate(input, { onSuccess: (r) => { setResult(r); if (r.models?.length) setModels(r.models); setPassedKey(r.passed ? key : null) } })}
            testing={test.isPending} result={result} testError={test.error?.message}
            actions={started ? <Button variant="ghost" onClick={clear}>Clear section</Button> : undefined} />
          {started && !tested && !test.isPending && (
            <p role="note" className="rounded-lg bg-warn-bg p-3 text-sm text-warn-fg">
              You started configuring a code agent. Run Test Configuration to continue, or clear this section to create the workspace without one.
            </p>
          )}
        </div>
      )}
    </section>
  )
}
