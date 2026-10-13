import clsx from 'clsx'
import { useState } from 'react'
import { Link } from 'react-router-dom'
import type { Workspace } from '@/api/types'
import { StatusBadge, type StateKey } from '@/components/ui/StatusBadge'
import { osLabel, relativeTime } from '@/lib/format'

export const PROVISION_STEPS = ['allocating', 'preparing', 'starting', 'ready'] as const
const STEP_LABEL: Record<string, string> = {
  allocating: 'Allocating resources',
  preparing: 'Preparing environment',
  starting: 'Starting workspace',
  ready: 'Ready',
}

export const isProvisioning = (w: Workspace) => ['allocating', 'preparing', 'starting'].includes(w.status)

function badgeState(w: Workspace): StateKey {
  if (isProvisioning(w)) return 'provisioning'
  return w.status === 'ready' ? 'running' : (w.status as StateKey)
}

interface Props {
  workspace: Workspace
  view: 'grid' | 'list'
  onStart: (w: Workspace) => void
  onStop: (w: Workspace) => void
  onDelete: (w: Workspace) => void
  onViewError: (w: Workspace) => void
}

export function WorkspaceCard({ workspace: w, view, onStart, onStop, onDelete, onViewError }: Props) {
  const [menu, setMenu] = useState(false)
  const provisioning = isProvisioning(w)
  const stepIdx = Math.max(0, PROVISION_STEPS.indexOf(w.step as (typeof PROVISION_STEPS)[number])) + 1

  const action =
    w.status === 'ready' ? <Link className="text-sm font-semibold text-blue hover:underline" to={`/w/${w.id}`}>Open</Link>
    : w.status === 'stopped' ? <button className="text-sm font-semibold text-blue hover:underline" onClick={() => onStart(w)}>Start</button>
    : w.status === 'failed' ? <button className="text-sm font-semibold text-blue hover:underline" onClick={() => onViewError(w)}>View error</button>
    : <Link className="text-sm font-semibold text-blue hover:underline" to={`/w/${w.id}/provisioning`}>View progress</Link>

  return (
    <article
      aria-label={w.name}
      className={clsx(
        'rounded-card border bg-panel p-5 shadow-[0_1px_2px_rgba(11,21,48,0.04)]',
        w.status === 'failed' ? 'border-err-fg/40' : 'border-line',
        view === 'list' && 'flex items-center gap-6',
      )}
    >
      <div className={clsx('flex items-start justify-between', view === 'list' && 'w-64 shrink-0')}>
        <div>
          <h3 className="text-[17px] font-extrabold">{w.name}</h3>
          <p className="mt-0.5 font-mono text-[11px] uppercase tracking-wide text-muted">{osLabel(w.os)}</p>
        </div>
        <div className="relative">
          <button aria-label={`Actions for ${w.name}`} aria-haspopup="menu" aria-expanded={menu} onClick={() => setMenu((m) => !m)} className="rounded-md px-2 text-lg leading-none text-muted hover:text-fg">···</button>
          {menu && (
            <ul role="menu" className="absolute right-0 z-10 mt-1 w-40 rounded-lg border border-line bg-panel py-1 text-sm shadow-lg" onMouseLeave={() => setMenu(false)}>
              {w.status === 'ready' && <li><Link role="menuitem" className="block px-3 py-1.5 hover:bg-neutral-bg" to={`/w/${w.id}`}>Open</Link></li>}
              <li><Link role="menuitem" className="block px-3 py-1.5 hover:bg-neutral-bg" to={`/w/${w.id}/settings`}>Settings</Link></li>
              {w.status === 'ready' && <li><button role="menuitem" className="w-full px-3 py-1.5 text-left hover:bg-neutral-bg" onClick={() => { setMenu(false); onStop(w) }}>Stop</button></li>}
              {w.status === 'stopped' && <li><button role="menuitem" className="w-full px-3 py-1.5 text-left hover:bg-neutral-bg" onClick={() => { setMenu(false); onStart(w) }}>Start</button></li>}
              <li><button role="menuitem" className="w-full px-3 py-1.5 text-left text-err-fg hover:bg-neutral-bg" onClick={() => { setMenu(false); onDelete(w) }}>Delete…</button></li>
            </ul>
          )}
        </div>
      </div>

      <div className={clsx('flex-1', view === 'grid' && 'mt-4')}>
        {provisioning ? (
          <div>
            <div className="flex justify-between text-sm font-semibold"><span>{STEP_LABEL[w.step] ?? w.step}</span><span className="font-mono text-xs text-muted">{stepIdx} / 4</span></div>
            <div role="progressbar" aria-valuemin={0} aria-valuemax={4} aria-valuenow={stepIdx} aria-label="Provisioning progress" className="mt-2 h-1.5 overflow-hidden rounded-full bg-neutral-bg">
              <div className="h-full rounded-full bg-blue transition-[width] duration-300" style={{ width: `${(stepIdx / 4) * 100}%` }} />
            </div>
            <p className="mt-3 text-[13px] text-muted">{w.cpu} vCPU · {w.ramGb} GB · {w.storageGb} GB</p>
          </div>
        ) : w.status === 'failed' ? (
          <div>
            <p className="rounded-lg bg-err-bg p-3 font-mono text-[12px] leading-snug text-err-fg">{w.error || 'Provisioning failed.'}</p>
            <p className="mt-3 text-[13px] text-muted">{w.cpu} vCPU · {w.ramGb} GB · {w.storageGb} GB</p>
          </div>
        ) : (
          <div>
            <div className="grid grid-cols-3 gap-2">
              {[[w.cpu, 'vCPU'], [w.ramGb, 'GB'], [w.storageGb, 'GB']].map(([v, u], i) => (
                <div key={i} className="rounded-lg bg-neutral-bg px-3 py-1.5 font-mono text-[13px]">{v} <span className="text-faint">{u}</span></div>
              ))}
            </div>
            {w.summary && <p className="mt-3 text-[13px] text-muted">{w.summary}</p>}
          </div>
        )}
      </div>

      <div className={clsx('flex items-center justify-between', view === 'grid' ? 'mt-4 border-t border-line pt-4' : 'w-80 shrink-0')}>
        <div className="flex items-center gap-2.5">
          <StatusBadge state={badgeState(w)} />
          <span className="text-xs text-faint">{provisioning ? `Started ${relativeTime(w.createdAt)}` : w.status === 'failed' ? relativeTime(w.lastOpenedAt) : `Opened ${relativeTime(w.lastOpenedAt)}`}</span>
        </div>
        {action}
      </div>
    </article>
  )
}
