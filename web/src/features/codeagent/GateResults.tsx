import { useQueryClient } from '@tanstack/react-query'
import clsx from 'clsx'
import { useEffect } from 'react'
import { Button } from '@/components/ui/Button'
import { useCancelGates, useGateRun, useStartGates, type GateResult } from './hooks'

const STATUS_LABEL: Record<GateResult['status'], string> = { passed: 'Passed', failed: 'Failed', timeout: 'Timed out', error: 'Error', skipped: 'Skipped' }

export function GateStatusChip({ status }: { status: GateResult['status'] | 'running' }) {
  const ok = status === 'passed'
  const bad = status === 'failed' || status === 'timeout' || status === 'error'
  return (
    <span className={clsx('rounded-full px-2 py-0.5 text-[11px] font-semibold', ok && 'bg-ok-bg text-ok-fg', bad && 'bg-err-bg text-err-fg', !ok && !bad && 'bg-raised text-muted')}>
      {status === 'running' ? 'Running…' : STATUS_LABEL[status]}
    </span>
  )
}

/** Results of one gate run, per gate with its log (PRD F23). */
export function GateResults({ workspaceId, runId }: { workspaceId: string; runId: string }) {
  const { data: run } = useGateRun(workspaceId, runId)
  const qc = useQueryClient()
  const finished = run && run.status !== 'running'
  // The change set's gate verdict is computed on the server; refresh it once a run ends.
  useEffect(() => {
    if (finished) {
      qc.invalidateQueries({ queryKey: ['change', workspaceId] })
      qc.invalidateQueries({ queryKey: ['changes', workspaceId] })
    }
  }, [finished, run?.id, qc, workspaceId])
  if (!run) return <p className="text-xs text-muted">Loading results…</p>
  return (
    <div aria-label="Quality gate results">
      {run.status === 'running' && <p role="status" className="text-xs text-muted">Running gates… the proposed change is applied temporarily while they run.</p>}
      {run.status === 'error' && <p role="alert" className="text-xs text-err-fg">The gates could not run: {run.error}</p>}
      {run.status === 'interrupted' && <p className="text-xs text-muted">This run was interrupted. Run the gates again.</p>}
      <ul className="mt-2 space-y-1.5">
        {run.results.map((r) => (
          <li key={r.gateId} className="rounded-lg border border-line bg-bg">
            <details>
              <summary className="flex cursor-pointer items-center gap-2 px-3 py-2 text-[13px]">
                <GateStatusChip status={r.status} />
                <span className="font-semibold">{r.name}</span>
                <span className="text-xs text-faint">{r.policy}</span>
                <span className="ml-auto text-xs text-faint">{r.millis} ms</span>
              </summary>
              <pre aria-label={`Log for ${r.name}`} className="max-h-56 overflow-auto whitespace-pre-wrap border-t border-line px-3 py-2 font-mono text-[11.5px] text-muted">{r.log || '(no output)'}</pre>
            </details>
          </li>
        ))}
      </ul>
      {run.status === 'done' && (
        <p className={clsx('mt-2 text-xs font-semibold', run.passed ? 'text-ok-fg' : 'text-err-fg')}>
          {run.passed ? 'Every required gate passed.' : `Required gates not passed: ${run.requiredFailed.join(', ')}.`}
        </p>
      )}
    </div>
  )
}

/** Run button plus live results for a workspace or a change set. */
export function RunGates({ workspaceId, setId, runId, onStarted, label = 'Run quality gates' }: { workspaceId: string; setId?: string; runId?: string | null; onStarted?: (id: string) => void; label?: string }) {
  const start = useStartGates(workspaceId)
  const cancel = useCancelGates(workspaceId)
  const { data: run } = useGateRun(workspaceId, runId ?? null)
  const running = run?.status === 'running' || start.isPending
  return (
    <div className="flex items-center gap-2">
      {running && runId ? (
        <Button size="sm" onClick={() => cancel.mutate(runId)}>Cancel</Button>
      ) : (
        <Button size="sm" loading={start.isPending} onClick={() => start.mutate({ setId }, { onSuccess: (r) => onStarted?.(r.id) })}>{runId ? 'Run again' : label}</Button>
      )}
      {start.error && <span role="alert" className="text-xs text-err-fg">{start.error.message}</span>}
    </div>
  )
}
