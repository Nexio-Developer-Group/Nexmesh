import clsx from 'clsx'
import { useEffect, useRef, useState } from 'react'
import { ApiError } from '@/api/client'
import type { CanvasComponent } from '@/api/types'
import { Button } from '@/components/ui/Button'
import { CardMetrics } from '@/features/metrics/CardMetrics'
import { useHealth, useLogs, type HealthSample } from './hooks'

const LABEL: Record<string, string> = {
  running: 'Running', connected: 'Connected', ready: 'Ready', stopped: 'Stopped', degraded: 'Degraded', failed: 'Failed', installing: 'Installing', starting: 'Starting',
  disconnected: 'Disconnected', test_failed: 'Test failed', draft: 'Draft',
}
const label = (s: string) => LABEL[s] ?? s

function since(iso?: string) {
  if (!iso) return ''
  const m = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 60000))
  if (m < 1) return 'just now'
  if (m < 60) return `${m} min`
  if (m < 48 * 60) return `${Math.round(m / 60)} h`
  return `${Math.round(m / 1440)} d`
}

/** Health timeline (every service card) and logs (installed services). */
export function ComponentActivity({ workspaceId, component: c, hasLogs }: { workspaceId: string; component: CanvasComponent; hasLogs: boolean }) {
  return (
    <div className="space-y-3 border-t border-line p-5">
      <HealthTimeline workspaceId={workspaceId} component={c} />
      <CardMetrics workspaceId={workspaceId} componentId={c.id} />
      {hasLogs && <LogsView workspaceId={workspaceId} component={c} />}
    </div>
  )
}

function HealthTimeline({ workspaceId, component: c }: { workspaceId: string; component: CanvasComponent }) {
  const { data, isPending, error } = useHealth(workspaceId, c.id, c.observedState)
  // Oldest first, left to right.
  const samples = data ? [...data.samples].reverse() : []
  return (
    <section aria-label="Health history">
      <h3 className="text-[11px] font-bold uppercase tracking-wider text-faint">Health</h3>
      {isPending && <p className="mt-2 text-sm text-muted">Loading…</p>}
      {error instanceof ApiError && <p role="alert" className="mt-2 text-sm text-err-fg">{error.message}</p>}
      {data && samples.length === 0 && <p className="mt-2 text-sm text-muted">No checks recorded yet.</p>}
      {data && samples.length > 0 && (
        <>
          <p className="mt-1 text-sm">
            <span className="font-semibold">{label(data.samples[0].state)}</span>
            <span className="text-muted"> for {since(data.since)} · {Math.round(data.uptimePercent)}% healthy in this window</span>
          </p>
          <div role="img" aria-label={`Health timeline of the last ${samples.length} checks, oldest to newest`} className="mt-2 flex h-6 items-end gap-[2px]">
            {samples.map((s, i) => <Block key={s.at + i} s={s} />)}
          </div>
          <ol aria-label="Recent state changes" className="mt-3 space-y-1 text-[12.5px]">
            {changes(data.samples).slice(0, 5).map((s) => (
              <li key={s.at} className="flex items-baseline gap-2">
                <span aria-hidden className={s.healthy ? 'text-ok-fg' : 'text-err-fg'}>{s.healthy ? '●' : '▲'}</span>
                <span className="font-semibold">{label(s.state)}</span>
                {s.message && <span className="min-w-0 flex-1 truncate text-muted" title={s.message}>{s.message}</span>}
                <time dateTime={s.at} className="ml-auto shrink-0 text-faint">{new Date(s.at).toLocaleTimeString()}</time>
              </li>
            ))}
          </ol>
        </>
      )}
    </section>
  )
}

/** Samples that begin a new state (the list is newest first). */
function changes(samples: HealthSample[]) {
  return samples.filter((s, i) => i === samples.length - 1 || samples[i + 1].state !== s.state)
}

function Block({ s }: { s: HealthSample }) {
  return <span title={`${label(s.state)} · ${new Date(s.at).toLocaleString()}${s.latencyMs ? ` · ${s.latencyMs} ms` : ''}`}
    className={clsx('h-full min-w-[3px] flex-1 rounded-[2px]', s.healthy ? 'bg-ok-fg/80' : 'bg-err-fg')} />
}

function LogsView({ workspaceId, component: c }: { workspaceId: string; component: CanvasComponent }) {
  const [open, setOpen] = useState(false)
  const [follow, setFollow] = useState(true)
  const logs = useLogs(workspaceId, c.id, open, follow)
  const box = useRef<HTMLPreElement>(null)
  useEffect(() => { if (follow && box.current) box.current.scrollTop = box.current.scrollHeight }, [logs.data, follow])
  const err = logs.error instanceof ApiError ? logs.error : null
  return (
    <section aria-label="Logs">
      <div className="flex items-center">
        <h3 className="text-[11px] font-bold uppercase tracking-wider text-faint">Logs</h3>
        <Button size="sm" variant="ghost" className="ml-auto" aria-expanded={open} onClick={() => setOpen((o) => !o)}>{open ? 'Hide logs' : 'Show logs'}</Button>
      </div>
      {open && (
        <div className="mt-2">
          <div className="mb-1.5 flex items-center gap-3 text-xs text-muted">
            <label className="flex items-center gap-1.5"><input type="checkbox" checked={follow} onChange={(e) => setFollow(e.target.checked)} /> Follow</label>
            <button type="button" className="font-semibold text-link hover:underline" onClick={() => logs.refetch()}>Refresh</button>
          </div>
          {err && <p role="alert" className="text-sm text-err-fg">{err.message}</p>}
          {logs.isPending && <p role="status" className="text-sm text-muted">Loading logs…</p>}
          {logs.data !== undefined && (
            <pre ref={box} aria-label="Service logs" tabIndex={0} className="max-h-64 overflow-auto whitespace-pre-wrap rounded-lg border border-line bg-bg p-3 font-mono text-[11.5px] leading-5">
              {logs.data.trim() === '' ? 'No output yet.' : logs.data}
            </pre>
          )}
        </div>
      )}
    </section>
  )
}
