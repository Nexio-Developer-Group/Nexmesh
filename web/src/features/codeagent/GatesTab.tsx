import { useEffect, useState } from 'react'
import { Button } from '@/components/ui/Button'
import { Field } from '@/components/ui/Field'
import { GateResults, RunGates } from './GateResults'
import { useDetectGates, useGateRuns, useGates, useSaveGates, type Gate, type GateKind } from './hooks'

const KINDS: GateKind[] = ['format', 'lint', 'typecheck', 'test', 'build', 'secrets', 'deps', 'custom']

/**
 * Quality gates (PRD F23). Detection only suggests; nothing counts until the
 * user reviews the list and saves it. A required gate that fails blocks
 * acceptance of an agent change set unless overridden, and the override is recorded.
 */
export function GatesTab({ workspaceId }: { workspaceId: string }) {
  const { data: cfg, isPending } = useGates(workspaceId)
  const save = useSaveGates(workspaceId)
  const detect = useDetectGates(workspaceId)
  const { data: runs = [] } = useGateRuns(workspaceId)
  const [gates, setGates] = useState<Gate[] | null>(null)
  const [runId, setRunId] = useState<string | null>(null)
  useEffect(() => { if (cfg && gates === null) setGates(cfg.gates) }, [cfg, gates])
  const list = gates ?? []
  const dirty = !!cfg && JSON.stringify(cfg.gates) !== JSON.stringify(list)
  const update = (i: number, patch: Partial<Gate>) => setGates(list.map((g, j) => (j === i ? { ...g, ...patch } : g)))
  const lastRun = runId ?? runs.find((r) => !r.setId)?.id ?? null

  const addDetected = (found: Gate[]) => {
    const have = new Set(list.map((g) => g.id))
    setGates([...list, ...found.filter((g) => !have.has(g.id))])
  }
  return (
    <div className="max-w-4xl">
      <h1 className="text-xl font-extrabold">Quality gates</h1>
      <p className="mt-1 text-sm text-muted">Checks that run in your workspace before an agent change is accepted. A <strong>required</strong> gate that fails blocks acceptance unless you override it with a reason. <strong>Advisory</strong> gates only report. The agent cannot change this list.</p>
      <div className="mt-4 flex flex-wrap items-center gap-2">
        <Button loading={detect.isPending} onClick={() => detect.mutate(undefined, { onSuccess: addDetected })}>Detect project tooling</Button>
        <Button onClick={() => setGates([...list, { id: `custom-${list.length + 1}`, name: 'My check', kind: 'custom', command: '', policy: 'advisory', enabled: true, timeoutSec: 120 }])}>Add a gate</Button>
        <Button variant="primary" disabled={!dirty && !!cfg?.confirmed} loading={save.isPending} onClick={() => save.mutate(list, { onSuccess: (c) => setGates(c.gates) })}>{cfg?.confirmed ? 'Save gates' : 'Confirm and save gates'}</Button>
      </div>
      {detect.error && <p role="alert" className="mt-2 text-sm text-err-fg">{detect.error.message}</p>}
      {save.error && <p role="alert" className="mt-2 text-sm text-err-fg">{save.error.message}</p>}
      {detect.isSuccess && <p role="status" className="mt-2 text-sm text-muted">Detected tooling was added below. Review it, then save to confirm.</p>}
      {isPending && <p className="mt-4 text-sm text-muted">Loading…</p>}
      {cfg && !cfg.confirmed && list.length === 0 && <p className="mt-6 text-sm text-muted">No gates yet. Detect your project’s tooling, or add your own.</p>}

      <ul className="mt-4 space-y-3" aria-label="Gates">
        {list.map((g, i) => (
          <li key={g.id} className="rounded-card border border-line p-3">
            <div className="grid gap-3 md:grid-cols-[1fr_140px_150px_auto]">
              <Field label="Name" value={g.name} onChange={(e) => update(i, { name: e.target.value })} />
              <div><label htmlFor={`kind-${i}`} className="mb-1.5 block text-[13px] font-semibold text-fg">Kind</label>
                <select id={`kind-${i}`} value={g.kind} onChange={(e) => update(i, { kind: e.target.value as GateKind })} className="h-10 w-full rounded-[10px] border border-line-strong bg-field px-2 text-sm text-fg">
                  {KINDS.map((k) => <option key={k}>{k}</option>)}</select></div>
              <div><label htmlFor={`policy-${i}`} className="mb-1.5 block text-[13px] font-semibold text-fg">Policy</label>
                <select id={`policy-${i}`} value={g.policy} onChange={(e) => update(i, { policy: e.target.value as Gate['policy'] })} className="h-10 w-full rounded-[10px] border border-line-strong bg-field px-2 text-sm text-fg">
                  <option value="required">Required</option><option value="advisory">Advisory</option></select></div>
              <div className="flex items-end gap-3 pb-2">
                <label className="flex items-center gap-1.5 text-[13px]"><input type="checkbox" checked={g.enabled} onChange={(e) => update(i, { enabled: e.target.checked })} aria-label={`Enable ${g.name}`} />On</label>
                <button className="text-xs text-err-fg underline" onClick={() => setGates(list.filter((_, j) => j !== i))} aria-label={`Remove ${g.name}`}>Remove</button>
              </div>
            </div>
            <div className="mt-2 grid gap-3 md:grid-cols-[1fr_120px]">
              <Field label="Command" mono value={g.command} onChange={(e) => update(i, { command: e.target.value })} hint={g.command.startsWith('builtin:') ? 'Built in: runs without any tool installed in the workspace.' : g.detected ? `Detected: ${g.detected}` : 'Runs in the workspace folder with its variables.'} />
              <Field label="Timeout (s)" type="number" min={5} max={600} value={g.timeoutSec} onChange={(e) => update(i, { timeoutSec: Number(e.target.value) })} />
            </div>
          </li>
        ))}
      </ul>

      {cfg?.confirmed && cfg.gates.length > 0 && (
        <section aria-label="Run gates" className="mt-6">
          <div className="flex items-center gap-3"><h2 className="text-base font-bold">Run now</h2>
            <RunGates workspaceId={workspaceId} runId={lastRun} onStarted={setRunId} label="Run all gates" /></div>
          <p className="mt-1 text-xs text-muted">Runs on the workspace as it is now. For a proposed change, run the gates from its review.</p>
          {lastRun && <div className="mt-3"><GateResults workspaceId={workspaceId} runId={lastRun} /></div>}
        </section>
      )}
    </div>
  )
}
