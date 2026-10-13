import clsx from 'clsx'
import { Handle, Position, type Node, type NodeProps } from '@xyflow/react'
import type { CanvasComponent, CatalogComponent } from '@/api/types'
import { ModeBadge, StatusBadge } from '@/components/ui/StatusBadge'

export interface TechNodeData extends Record<string, unknown> {
  component: CanvasComponent
  def: CatalogComponent
  selected: boolean
  onRetry?: (c: CanvasComponent) => void
  onViewLogs?: (c: CanvasComponent) => void
}
export type TechNodeType = Node<TechNodeData, 'tech'>

export function Monogram({ text, size = 36 }: { text: string; size?: number }) {
  return (
    <span aria-hidden style={{ width: size, height: size }} className="grid shrink-0 place-items-center rounded-lg border border-line-strong bg-raised font-mono text-xs font-semibold text-info-fg">
      {text}
    </span>
  )
}

const border: Record<string, string> = {
  failed: 'border-err-fg/60',
  test_failed: 'border-err-fg/60',
  degraded: 'border-warn-fg/60',
}

/** A canvas card: a visual representation of a real configured resource (PRD 7.2). */
export function TechNode({ data }: NodeProps<TechNodeType>) {
  const { component: c, def, selected } = data
  const port = def.ports?.[0] ?? def.install?.ports?.[0]
  const sub = [c.version, port ? `:${port}` : null].filter(Boolean).join(' · ')
  const installing = c.observedState === 'installing'
  return (
    <div
      role="group"
      aria-label={`${def.name} card, ${c.observedState}`}
      className={clsx(
        'w-[216px] rounded-card border bg-panel p-3.5 text-fg shadow-[0_8px_24px_-12px_rgba(0,0,0,0.6)] transition-shadow',
        selected ? 'border-blue shadow-[0_0_0_3px_rgba(22,118,254,0.28)]' : (border[c.observedState] ?? 'border-line-strong'),
      )}
    >
      <Handle type="target" position={Position.Left} className="!pointer-events-none !opacity-0" />
      <Handle type="source" position={Position.Right} className="!pointer-events-none !opacity-0" />
      <div className="flex items-start gap-2.5">
        <Monogram text={def.monogram} />
        <div className="min-w-0 flex-1">
          <div className="flex items-center justify-between gap-1">
            <p className="truncate text-[15px] font-bold">{c.connName || def.name}</p>
            {c.environment && (
              <span className={clsx('rounded px-1.5 py-0.5 text-[10px] font-semibold', c.environment === 'production' ? 'bg-warn-bg text-warn-fg' : 'bg-neutral-bg text-neutral-fg')}>{c.environment}</span>
            )}
          </div>
          <p className="truncate font-mono text-[11px] text-muted">{sub || def.kind}</p>
        </div>
      </div>

      {installing && (
        <div className="mt-3">
          <div role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={c.progress} aria-label={`${def.name} install progress`} className="h-1 overflow-hidden rounded-full bg-neutral-bg">
            <div className="h-full rounded-full bg-blue transition-[width]" style={{ width: `${c.progress}%` }} />
          </div>
        </div>
      )}
      {c.message && <p className={clsx('mt-2.5 font-mono text-[10.5px] leading-snug', c.observedState === 'failed' || c.observedState === 'test_failed' ? 'text-err-fg' : 'text-faint')}>{c.message}</p>}

      <div className="mt-3 flex flex-wrap items-center gap-1.5">
        <StatusBadge state={c.observedState} label={installing ? `Installing ${c.progress}%` : undefined} />
        <ModeBadge mode={c.mode} />
      </div>

      {c.observedState === 'failed' && c.mode === 'installed' && (
        <div className="nodrag mt-3 flex gap-2">
          <button className="h-8 flex-1 rounded-lg border border-line-strong bg-raised text-xs font-semibold hover:border-blue" onClick={() => data.onRetry?.(c)}>Retry</button>
          <button className="h-8 flex-1 text-xs text-muted hover:text-fg" onClick={() => data.onViewLogs?.(c)}>View logs</button>
        </div>
      )}
    </div>
  )
}
