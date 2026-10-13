import clsx from 'clsx'

/** Every state has a dot or icon AND a text label: never colour alone (PRD 19.4). */
export type StateKey =
  | 'draft' | 'installing' | 'running' | 'stopped' | 'failed'
  | 'testing' | 'test_passed' | 'test_failed' | 'connected' | 'degraded' | 'disconnected'
  | 'provisioning' | 'ready'

const META: Record<StateKey, { label: string; tone: 'ok' | 'err' | 'warn' | 'info' | 'neutral'; icon: 'dot' | 'x' | 'warn' | 'spin' }> = {
  draft: { label: 'Draft', tone: 'neutral', icon: 'dot' },
  installing: { label: 'Installing', tone: 'info', icon: 'spin' },
  running: { label: 'Running', tone: 'ok', icon: 'dot' },
  ready: { label: 'Ready', tone: 'ok', icon: 'dot' },
  stopped: { label: 'Stopped', tone: 'neutral', icon: 'dot' },
  failed: { label: 'Failed', tone: 'err', icon: 'x' },
  testing: { label: 'Testing', tone: 'info', icon: 'spin' },
  test_passed: { label: 'Test passed', tone: 'ok', icon: 'dot' },
  test_failed: { label: 'Test failed', tone: 'err', icon: 'x' },
  connected: { label: 'Connected', tone: 'ok', icon: 'dot' },
  degraded: { label: 'Degraded', tone: 'warn', icon: 'warn' },
  disconnected: { label: 'Disconnected', tone: 'neutral', icon: 'dot' },
  provisioning: { label: 'Provisioning', tone: 'info', icon: 'spin' },
}

const tones = {
  ok: 'bg-ok-bg text-ok-fg',
  err: 'bg-err-bg text-err-fg',
  warn: 'bg-warn-bg text-warn-fg',
  info: 'bg-info-bg text-info-fg',
  neutral: 'bg-neutral-bg text-neutral-fg',
}

export function StatusBadge({ state, label, className }: { state: StateKey; label?: string; className?: string }) {
  const m = META[state]
  return (
    <span className={clsx('inline-flex h-[22px] items-center gap-1.5 rounded-md px-2 text-[11.5px] font-semibold', tones[m.tone], className)}>
      {m.icon === 'dot' && <span aria-hidden className="size-1.5 rounded-full bg-current" />}
      {m.icon === 'x' && <span aria-hidden>✕</span>}
      {m.icon === 'warn' && <span aria-hidden>▲</span>}
      {m.icon === 'spin' && (
        <span aria-hidden className="size-2.5 rounded-full border-[1.5px] border-current border-t-transparent" style={{ animation: 'nx-spin 0.9s linear infinite' }} />
      )}
      {label ?? m.label}
    </span>
  )
}

export function ModeBadge({ mode, pendingConnect }: { mode: 'installed' | 'connected' | ''; pendingConnect?: boolean }) {
  if (!mode && pendingConnect) return <span className="inline-flex h-[22px] items-center rounded-md border border-dashed border-line-strong px-2 text-[11px] text-faint">Connect · not added</span>
  if (!mode) return <span className="inline-flex h-[22px] items-center rounded-md border border-dashed border-line-strong px-2 text-[11px] text-faint">Install or Connect</span>
  return (
    <span className="inline-flex h-[22px] items-center rounded-md border border-line-strong px-2 text-[11px] font-medium text-muted">
      {mode === 'installed' ? 'Installed' : 'Connected'}
    </span>
  )
}
