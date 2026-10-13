import clsx from 'clsx'
import type { StageResult, TestResult } from '@/api/types'
import { STAGES } from './hooks'

function Icon({ status }: { status: StageResult['status'] }) {
  const base = 'grid size-[18px] shrink-0 place-items-center rounded-full text-[10px] font-bold'
  switch (status) {
    case 'passed': return <span aria-hidden className={clsx(base, 'bg-ok-bg text-ok-fg')}>✓</span>
    case 'failed': return <span aria-hidden className={clsx(base, 'bg-err-bg text-err-fg')}>✕</span>
    case 'running': return <span aria-hidden className={clsx(base, 'text-info-fg')}><span className="size-3 rounded-full border-2 border-current border-t-transparent" style={{ animation: 'nx-spin .8s linear infinite' }} /></span>
    case 'skipped': return <span aria-hidden className={clsx(base, 'bg-neutral-bg text-faint')}>–</span>
    default: return <span aria-hidden className={clsx(base, 'border border-line-strong text-faint')} />
  }
}

const STATUS_WORD: Record<StageResult['status'], string> = { passed: 'passed', failed: 'failed', running: 'running', skipped: 'skipped', pending: 'pending' }

/** Each check, in order, with its own pass/fail and specific message (PRD 11.5). */
export function TestResultList({ result, running }: { result?: TestResult | null; running?: boolean }) {
  const byStage = new Map((result?.stages ?? []).map((s) => [s.stage, s]))
  // A skipped stage on a passing result means "not applicable" (for example TLS is off), so it counts.
  const done = result?.passed ? STAGES.length : (result?.stages.filter((s) => s.status === 'passed').length ?? 0)
  const summary = running ? 'Testing…' : !result?.at ? '' : result.passed ? `Passed · ${done} of ${STAGES.length} checks` : `Failed at ${STAGES.find((s) => s.key === result.failedStage)?.label ?? 'a check'}`
  return (
    <section aria-label="Connection test">
      <div className="mb-2 flex items-baseline justify-between">
        <h3 className="text-[13px] font-semibold">Connection test</h3>
        <span role="status" className={clsx('text-xs', result?.passed ? 'text-ok-fg' : result && !running && result.failedStage ? 'text-err-fg' : 'text-muted')}>{summary}</span>
      </div>
      <ol className="space-y-2.5">
        {STAGES.map((s) => {
          const r = byStage.get(s.key)
          const status = r?.status ?? 'pending'
          return (
            <li key={s.key} className="flex gap-2.5">
              <Icon status={status} />
              <div className="min-w-0">
                <p className="text-sm font-medium">{s.label}<span className="sr-only"> {STATUS_WORD[status]}</span></p>
                {r?.message && status !== 'pending' && status !== 'running' && (
                  <p className={clsx('break-words font-mono text-[11px] leading-snug', status === 'failed' ? 'text-err-fg' : 'text-muted')}>{r.message}</p>
                )}
              </div>
            </li>
          )
        })}
      </ol>
      {result?.warnings?.map((w) => <p key={w} role="note" className="mt-3 rounded-lg bg-warn-bg p-2.5 text-xs text-warn-fg">{w}</p>)}
    </section>
  )
}
