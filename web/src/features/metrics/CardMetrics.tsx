import { Sparkline } from './Sparkline'
import { useMetrics, type MetricCard } from './hooks'

const fmtMb = (mb: number) => (mb >= 1024 ? `${(mb / 1024).toFixed(1)} GB` : `${Math.round(mb)} MB`)

function Bar({ value, max, label }: { value: number; max: number; label: string }) {
  const pct = max > 0 ? Math.min(100, Math.round((value / max) * 100)) : 0
  return (
    <div role="progressbar" aria-label={label} aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct} className="mt-1 h-1.5 overflow-hidden rounded-full bg-neutral-bg">
      <div className={pct > 85 ? 'h-full bg-warn-fg' : 'h-full bg-blue'} style={{ width: `${pct}%` }} />
    </div>
  )
}

/** CPU, memory and storage for an installed card; latency for a connected one (PRD F5). */
export function CardMetrics({ workspaceId, componentId }: { workspaceId: string; componentId: string }) {
  const { data, isPending, error } = useMetrics(workspaceId)
  const card: MetricCard | undefined = data?.components.find((c) => c.id === componentId)
  return (
    <section aria-label="Metrics">
      <h3 className="text-[11px] font-bold uppercase tracking-wider text-faint">Metrics</h3>
      {isPending && <p className="mt-2 text-sm text-muted">Loading…</p>}
      {error && <p role="alert" className="mt-2 text-sm text-err-fg">{error.message}</p>}
      {data && !card && <p className="mt-2 text-sm text-muted">No readings for this card.</p>}
      {card?.unavailable && !card.current && <p className="mt-2 text-sm text-muted">{card.unavailable}</p>}
      {card?.current && card.mode === 'installed' && (
        <div className="mt-2 space-y-3 text-[13px]">
          <div>
            <div className="flex items-baseline justify-between"><span>CPU</span><span className="font-mono">{card.current.cpuPercent}%</span></div>
            <Sparkline values={card.history.map((s) => s.cpuPercent)} label="CPU" unit="%" />
          </div>
          <div>
            <div className="flex items-baseline justify-between"><span>Memory</span><span className="font-mono">{fmtMb(card.current.memUsedMb)} of {fmtMb(card.current.memLimitMb)}</span></div>
            <Bar value={card.current.memUsedMb} max={card.current.memLimitMb} label="Memory used" />
            <div className="mt-1"><Sparkline values={card.history.map((s) => s.memUsedMb)} label="Memory" unit=" MB" max={card.current.memLimitMb} /></div>
          </div>
          <div className="flex items-baseline justify-between">
            <span>Storage</span>
            <span className="font-mono">{card.current.diskUsedMb >= 0 ? fmtMb(card.current.diskUsedMb) : 'not measurable'}</span>
          </div>
          {card.current.simulated && <p className="text-xs text-faint">Simulated runtime: these numbers are generated, not measured.</p>}
        </div>
      )}
      {card?.current && card.mode === 'connected' && (
        <div className="mt-2 text-[13px]">
          <div className="flex items-baseline justify-between"><span>Latency</span><span className="font-mono">{card.current.latencyMs ?? 0} ms</span></div>
          <Sparkline values={card.history.map((s) => s.latencyMs ?? 0)} label="Latency" unit=" ms" />
          <p className="mt-1 text-xs text-faint">From the health checks of the last {card.history.length} readings.</p>
        </div>
      )}
    </section>
  )
}

/** The workspace container itself, against its limits. */
export function WorkspaceUsage({ workspaceId, limits }: { workspaceId: string; limits: { cpu: number; ramGb: number; storageGb: number } }) {
  const { data } = useMetrics(workspaceId)
  const w = data?.workspace
  if (!w?.current) return null
  const c = w.current
  return (
    <section aria-label="Current usage" className="rounded-card border border-line bg-panel p-4">
      <h3 className="text-sm font-bold">Current usage</h3>
      <dl className="mt-2 grid gap-3 text-[13px] sm:grid-cols-3">
        <div><dt className="text-muted">CPU</dt><dd className="font-mono">{c.cpuPercent}% of {limits.cpu} vCPU</dd><Sparkline values={w.history.map((s) => s.cpuPercent)} label="CPU" unit="%" /></div>
        <div><dt className="text-muted">Memory</dt><dd className="font-mono">{fmtMb(c.memUsedMb)} of {limits.ramGb} GB</dd><Bar value={c.memUsedMb} max={limits.ramGb * 1024} label="Workspace memory used" /></div>
        <div><dt className="text-muted">Files</dt><dd className="font-mono">{c.diskUsedMb >= 0 ? fmtMb(c.diskUsedMb) : 'not measurable'} of {limits.storageGb} GB</dd>{c.diskUsedMb >= 0 && <Bar value={c.diskUsedMb} max={limits.storageGb * 1024} label="Workspace storage used" />}</div>
      </dl>
      {c.simulated && <p className="mt-2 text-xs text-faint">Simulated runtime: CPU and memory are generated; the file size is real.</p>}
    </section>
  )
}
