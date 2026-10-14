import clsx from 'clsx'
import { useMemo, useState } from 'react'
import { Button } from '@/components/ui/Button'
import { Modal } from '@/components/ui/Modal'
import { GateResults, RunGates } from './GateResults'
import { useAcceptChange, useChangeSet, useGateRuns, useRejectChange, type ChangeFile, type ChangeSet, type Decision } from './hooks'

const OP_LABEL = { create: 'New file', modify: 'Edited', delete: 'Deleted' } as const
const STATUS_LABEL: Record<ChangeSet['status'], string> = {
  proposed: 'Waiting for your review', applied: 'Applied', partially_applied: 'Partly applied', rejected: 'Rejected', reverted: 'Reverted', superseded: 'Replaced by a newer proposal',
}

function HunkView({ file, hunk, checked, onToggle, disabled }: { file: ChangeFile; hunk: ChangeFile['hunks'][number]; checked: boolean; onToggle: () => void; disabled: boolean }) {
  return (
    <div className={clsx('border-t border-line', !checked && !disabled && 'opacity-55')}>
      <label className="flex items-center gap-2 bg-raised px-3 py-1 text-[11.5px] text-muted">
        {!disabled && <input type="checkbox" checked={checked} onChange={onToggle} aria-label={`Accept change ${hunk.id + 1} in ${file.path}`} />}
        <span className="font-mono">@@ line {hunk.oldStart} @@</span>
        <span className="text-ok-fg">+{hunk.added}</span> <span className="text-err-fg">−{hunk.removed}</span>
      </label>
      <pre className="overflow-x-auto px-0 py-1 font-mono text-[12px] leading-[1.5]">
        {hunk.lines.map((l, i) => (
          <div key={i} className={clsx('whitespace-pre px-3', l.kind === 'add' && 'bg-ok-bg/60 text-ok-fg', l.kind === 'del' && 'bg-err-bg/60 text-err-fg', l.kind === 'context' && 'text-muted')}>
            <span aria-hidden className="mr-2 inline-block w-3 select-none text-faint">{l.kind === 'add' ? '+' : l.kind === 'del' ? '−' : ' '}</span>
            <span className="sr-only">{l.kind === 'add' ? 'Added: ' : l.kind === 'del' ? 'Removed: ' : ''}</span>{l.text || ' '}
          </div>
        ))}
      </pre>
    </div>
  )
}

/**
 * Change set review (PRD F18): diffs grouped by file, accept all, accept part
 * or reject. Nothing reaches the workspace until the user accepts.
 */
export function ReviewDialog({ workspaceId, setId, onClose, onHighlight }: { workspaceId: string; setId: string; onClose: () => void; onHighlight?: (componentIds: string[]) => void }) {
  const { data: set, isPending, error } = useChangeSet(workspaceId, setId)
  const accept = useAcceptChange(workspaceId, setId)
  const reject = useRejectChange(workspaceId, setId)
  const { data: runs = [] } = useGateRuns(workspaceId, setId)
  const [picked, setPicked] = useState<Record<string, Set<number>> | null>(null)
  const [runId, setRunId] = useState<string | null>(null)
  const [override, setOverride] = useState(false)
  const [reason, setReason] = useState('')

  const sel = useMemo(() => {
    if (!set) return {}
    if (picked) return picked
    return Object.fromEntries(set.files.map((f) => [f.path, new Set(f.hunks.map((h) => h.id))]))
  }, [set, picked])

  if (isPending) return <Modal title="Review changes" onClose={onClose} width={980}><p>Loading…</p></Modal>
  if (error || !set) return <Modal title="Review changes" onClose={onClose} width={980}><p role="alert" className="text-err-fg">{error?.message ?? 'Could not load this change set.'}</p></Modal>

  const open = set.status === 'proposed'
  const totalHunks = set.files.reduce((n, f) => n + f.hunks.length, 0)
  const chosenHunks = set.files.reduce((n, f) => n + (sel[f.path]?.size ?? 0), 0)
  const allChosen = chosenHunks === totalHunks
  const lastRun = runId ?? runs[0]?.id ?? null
  const gate = set.gate
  const blocked = !!gate && gate.configured && !(gate.ran && gate.passed)

  const toggle = (path: string, id: number) => {
    const next: Record<string, Set<number>> = Object.fromEntries(Object.entries(sel).map(([k, v]) => [k, new Set(v)]))
    const s = next[path] ?? new Set<number>()
    if (s.has(id)) s.delete(id); else s.add(id)
    next[path] = s
    setPicked(next)
  }
  const toggleFile = (f: ChangeFile) => {
    const next: Record<string, Set<number>> = Object.fromEntries(Object.entries(sel).map(([k, v]) => [k, new Set(v)]))
    next[f.path] = (sel[f.path]?.size ?? 0) === f.hunks.length ? new Set() : new Set(f.hunks.map((h) => h.id))
    setPicked(next)
  }
  const decision = (): Decision => {
    const d: Decision = allChosen ? { all: true } : { files: set.files.filter((f) => (sel[f.path]?.size ?? 0) > 0).map((f) => ({ path: f.path, hunks: (sel[f.path]?.size ?? 0) === f.hunks.length ? undefined : [...sel[f.path]] })) }
    if (blocked && override) d.overrideGates = reason.trim()
    return d
  }
  const done = () => onClose()

  return (
    <Modal title={set.title} onClose={onClose} width={980}
      footer={open ? (
        <>
          <Button variant="danger" onClick={() => reject.mutate(undefined, { onSuccess: done })} loading={reject.isPending}>Reject</Button>
          <Button variant="primary" disabled={chosenHunks === 0 || (blocked && (!override || reason.trim().length < 3))} loading={accept.isPending}
            onClick={() => accept.mutate(decision(), { onSuccess: done })}>
            {allChosen ? 'Accept all' : `Accept ${chosenHunks} of ${totalHunks} changes`}
          </Button>
        </>
      ) : <Button onClick={onClose}>Close</Button>}>
      <div className="max-h-[68vh] space-y-4 overflow-y-auto pr-1">
        <div className="flex flex-wrap items-center gap-2 text-xs">
          <span className="rounded-full bg-raised px-2 py-0.5 font-semibold text-fg">{set.actor === 'agent' ? 'Proposed by the Code Agent' : 'Proposed by you'}</span>
          <span className={clsx('rounded-full px-2 py-0.5 font-semibold', open ? 'bg-warn-bg text-warn-fg' : 'bg-raised text-muted')}>{STATUS_LABEL[set.status]}</span>
          <span className="text-faint">{set.files.length} file{set.files.length === 1 ? '' : 's'} · nothing is written until you accept</span>
        </div>
        {set.summary && <p className="text-[13px] text-fg">{set.summary}</p>}

        {set.warnings && set.warnings.length > 0 && (
          <ul role="alert" aria-label="Warnings" className="space-y-1 rounded-lg bg-warn-bg px-3 py-2 text-[13px] text-warn-fg">
            {set.warnings.map((w) => <li key={w}>{w}</li>)}
          </ul>
        )}

        <section aria-label="Impact" className="grid gap-3 text-[13px] md:grid-cols-2">
          <div>
            <h3 className="font-bold text-fg">Rules applied</h3>
            {set.rules.length === 0 ? <p className="text-muted">No rules applied.</p> : (
              <ul className="mt-1 space-y-0.5">{set.rules.map((r) => <li key={r.id}><span className="font-semibold text-fg">{r.title}</span> <span className="text-xs text-faint">{r.scope}</span></li>)}</ul>
            )}
          </div>
          <div>
            <h3 className="font-bold text-fg">Touches</h3>
            {set.impact.components.length === 0 && set.impact.variables.length === 0 ? <p className="text-muted">No components or variables are referenced.</p> : (
              <div className="mt-1 space-y-1">
                {set.impact.components.length > 0 && <p>Components: {set.impact.components.map((c) => c.name).join(', ')}</p>}
                {set.impact.variables.length > 0 && <p>Variables: <span className="font-mono text-xs">{set.impact.variables.join(', ')}</span></p>}
                {onHighlight && set.impact.components.length > 0 && (
                  <Button size="sm" onClick={() => onHighlight(set.impact.components.map((c) => c.id))}>Highlight in architecture view</Button>
                )}
              </div>
            )}
          </div>
        </section>

        {set.actor === 'agent' && gate && gate.configured && (
          <section aria-label="Quality gates" className="rounded-card border border-line p-3">
            <div className="flex flex-wrap items-center gap-3">
              <h3 className="text-[13px] font-bold text-fg">Quality gates</h3>
              <span className={clsx('text-xs font-semibold', gate.passed ? 'text-ok-fg' : gate.ran ? 'text-err-fg' : 'text-warn-fg')}>
                {gate.passed ? 'Required gates passed' : gate.ran ? `Required gates failed: ${gate.failing.join(', ')}` : 'Required gates have not run on this change'}
              </span>
              {open && <div className="ml-auto"><RunGates workspaceId={workspaceId} setId={set.id} runId={lastRun} onStarted={setRunId} /></div>}
            </div>
            {lastRun && <div className="mt-2"><GateResults workspaceId={workspaceId} runId={lastRun} /></div>}
            {open && blocked && (
              <div className="mt-3 border-t border-line pt-3">
                {!override ? (
                  <button className="text-xs font-semibold text-blue underline" onClick={() => setOverride(true)}>Override the gates…</button>
                ) : (
                  <div>
                    <label htmlFor="override-reason" className="block text-xs font-semibold text-fg">Reason for accepting despite the gates (recorded)</label>
                    <textarea id="override-reason" value={reason} onChange={(e) => setReason(e.target.value)} rows={2} className="mt-1 w-full rounded-lg border border-line-strong bg-field p-2 text-[13px] text-fg" />
                    <button className="mt-1 text-xs text-muted underline" onClick={() => { setOverride(false); setReason('') }}>Cancel override</button>
                  </div>
                )}
              </div>
            )}
          </section>
        )}
        {set.gateOverride && (
          <p className="rounded-lg bg-warn-bg px-3 py-2 text-[13px] text-warn-fg">Accepted despite failing gates ({set.gateOverride.failing.join(', ')}). Reason: {set.gateOverride.reason}</p>
        )}

        <section aria-label="Files">
          {set.files.map((f) => (
            <div key={f.path} className="mb-3 overflow-hidden rounded-card border border-line">
              <div className="flex items-center gap-3 bg-panel px-3 py-2 text-[13px]">
                {open && f.hunks.length > 0 && (
                  <input type="checkbox" aria-label={`Accept ${f.path}`} checked={(sel[f.path]?.size ?? 0) === f.hunks.length} onChange={() => toggleFile(f)} />
                )}
                <span className="font-mono font-semibold text-fg">{f.path}</span>
                <span className="text-xs text-muted">{OP_LABEL[f.op]}</span>
                <span className="text-xs text-ok-fg">+{f.added}</span><span className="text-xs text-err-fg">−{f.removed}</span>
                {!open && <span className="ml-auto text-xs text-muted">{f.status === 'applied' ? 'Applied' : f.status === 'partial' ? 'Partly applied' : 'Not applied'}</span>}
              </div>
              {f.hunks.map((h) => (
                <HunkView key={h.id} file={f} hunk={h} checked={sel[f.path]?.has(h.id) ?? false} onToggle={() => toggle(f.path, h.id)} disabled={!open} />
              ))}
            </div>
          ))}
        </section>
        {(accept.error || reject.error) && <p role="alert" className="text-sm text-err-fg">{(accept.error ?? reject.error)?.message}</p>}
      </div>
    </Modal>
  )
}
