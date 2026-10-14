import clsx from 'clsx'
import type { ReportItem } from './hooks'

const STYLE: Record<ReportItem['status'], { label: string; cls: string; icon: string }> = {
  applied: { label: 'Applied', cls: 'bg-ok-bg text-ok-fg', icon: '✓' },
  adapted: { label: 'Adapted', cls: 'bg-warn-bg text-warn-fg', icon: '≈' },
  unsupported: { label: 'Not supported', cls: 'bg-neutral-bg text-muted', icon: '✕' },
}

/** Every field of a dev container file with what happened to it. Nothing is hidden. */
export function ReportList({ report, label = 'Compatibility report' }: { report: ReportItem[]; label?: string }) {
  const counts = report.reduce((m, r) => ({ ...m, [r.status]: (m[r.status] ?? 0) + 1 }), {} as Record<string, number>)
  return (
    <section aria-label={label}>
      <p className="mb-2 text-sm font-semibold">
        {counts.applied ?? 0} applied · {counts.adapted ?? 0} adapted · {counts.unsupported ?? 0} not supported
      </p>
      <ul className="divide-y divide-line overflow-hidden rounded-lg border border-line">
        {report.map((r, i) => (
          <li key={r.field + i} className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5 px-3 py-2 text-[13px]">
            <span className={clsx('inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[11px] font-semibold', STYLE[r.status].cls)}><span aria-hidden>{STYLE[r.status].icon}</span>{STYLE[r.status].label}</span>
            <span className="font-mono text-[12.5px] font-semibold">{r.field}</span>
            <span className="w-full text-muted sm:w-auto sm:flex-1">{r.detail}</span>
          </li>
        ))}
      </ul>
    </section>
  )
}
