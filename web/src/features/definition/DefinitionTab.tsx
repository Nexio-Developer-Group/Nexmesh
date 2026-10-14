import clsx from 'clsx'
import { useState } from 'react'
import { ApiError } from '@/api/client'
import type { Workspace } from '@/api/types'
import { Button } from '@/components/ui/Button'
import { groupBy } from '@/lib/group'
import { useDevContainerReport, useDrift, type Difference } from './hooks'
import { ReportList } from './ReportList'

const KIND: Record<Difference['kind'], { label: string; cls: string }> = {
  changed: { label: 'Changed', cls: 'bg-warn-bg text-warn-fg' },
  missing: { label: 'Missing', cls: 'bg-err-bg text-err-fg' },
  extra: { label: 'Extra', cls: 'bg-info-bg text-info-fg' },
  unhealthy: { label: 'Unhealthy', cls: 'bg-err-bg text-err-fg' },
}
const CATEGORY: Record<string, string> = { workspace: 'Workspace', runtime: 'Runtimes', component: 'Components', variable: 'Variables', port: 'Ports', agent: 'Code agents' }

export function DefinitionTab({ workspace }: { workspace: Workspace }) {
  const drift = useDrift(workspace.id)
  const [showReport, setShowReport] = useState(false)
  const dcReport = useDevContainerReport(workspace.id, showReport)
  const [text, setText] = useState('')
  const err = drift.error instanceof ApiError ? drift.error : null
  const report = drift.data
  const groups = report ? groupBy(report.differences, (d) => d.category) : []

  return (
    <section aria-label="Definition" className="max-w-4xl space-y-8">
      <div>
        <h2 className="text-2xl font-extrabold tracking-tight">Definition</h2>
        <p className="mt-1 max-w-2xl text-muted">A definition file describes this workspace: resources, runtimes, components, connection settings, variable names, ports and layout. It never contains secret values, so it is safe to keep in Git and use to recreate the workspace elsewhere.</p>
      </div>

      <div className="rounded-card border border-line bg-panel p-5">
        <h3 className="font-bold">Export</h3>
        <p className="mt-1 text-sm text-muted">Credentials, secret variables and API keys are listed by name only. Import the file from the dashboard to create a matching workspace.</p>
        <div className="mt-3 flex gap-2">
          <a className="inline-flex h-10 items-center rounded-[10px] bg-nx-gradient px-4 text-sm font-semibold text-white" href={`/api/workspaces/${workspace.id}/definition`} download>Download YAML</a>
          <a className="inline-flex h-10 items-center rounded-[10px] border border-line-strong bg-raised px-4 text-sm font-semibold hover:border-blue" href={`/api/workspaces/${workspace.id}/definition?format=json`} download>Download JSON</a>
        </div>
      </div>

      <div className="rounded-card border border-line bg-panel p-5">
        <h3 className="font-bold">Dev container</h3>
        <p className="mt-1 text-sm text-muted">Export this workspace as a <code className="font-mono">devcontainer.json</code> that other tools (VS Code, GitHub Codespaces, the dev container CLI) can open. Runtimes, ports, variables and resources carry over; services and credentials do not.</p>
        <div className="mt-3 flex flex-wrap gap-2">
          <a className="inline-flex h-10 items-center rounded-[10px] bg-nx-gradient px-4 text-sm font-semibold text-white" href={`/api/workspaces/${workspace.id}/devcontainer`} download="devcontainer.json">Download devcontainer.json</a>
          <Button aria-expanded={showReport} onClick={() => setShowReport((s) => !s)}>{showReport ? 'Hide compatibility report' : 'Show compatibility report'}</Button>
        </div>
        {showReport && <div className="mt-4">{dcReport.isPending ? <p className="text-sm text-muted">Loading…</p> : dcReport.data ? <ReportList report={dcReport.data.report} label="Export compatibility report" /> : <p role="alert" className="text-sm text-err-fg">{dcReport.error?.message}</p>}</div>}
      </div>

      <div className="rounded-card border border-line bg-panel p-5">
        <h3 className="font-bold">Drift check</h3>
        <p className="mt-1 text-sm text-muted">Compare a definition file with this workspace as it is now. Every difference is listed.</p>
        <label htmlFor="drift-text" className="mt-3 block text-[13px] font-semibold">Definition</label>
        <textarea id="drift-text" rows={6} value={text} onChange={(e) => { setText(e.target.value); drift.reset() }} spellCheck={false}
          className="mt-1.5 w-full rounded-[10px] border border-line-strong bg-field p-3 font-mono text-[12px]" placeholder="Paste a definition, or choose a file" />
        <div className="mt-2 flex flex-wrap items-center gap-3">
          <label className="text-xs text-muted">Choose a file <input type="file" accept=".yaml,.yml,.json" aria-label="Choose a definition file" className="ml-1 text-xs" onChange={async (e) => { const f = e.target.files?.[0]; if (f) { setText(await f.text()); drift.reset() } }} /></label>
          <Button variant="primary" size="sm" className="ml-auto" disabled={!text.trim()} loading={drift.isPending} onClick={() => drift.mutate(text)}>Check for drift</Button>
        </div>
        {err && <p role="alert" className="mt-3 rounded-lg bg-err-bg p-3 text-sm text-err-fg">{err.message}</p>}
        {report?.inSync && <p role="status" className="mt-4 rounded-lg bg-ok-bg p-3 text-sm font-semibold text-ok-fg"><span aria-hidden>✓ </span>In sync. The workspace matches this definition.</p>}
        {report && !report.inSync && (
          <div className="mt-4 space-y-4">
            <p role="status" className="text-sm font-semibold">{report.differences.length} {report.differences.length === 1 ? 'difference' : 'differences'} from the definition</p>
            {groups.map(([cat, items]) => (
              <div key={cat}>
                <h4 className="mb-1.5 text-[11px] font-bold uppercase tracking-wider text-faint">{CATEGORY[cat] ?? cat}</h4>
                <ul className="divide-y divide-line overflow-hidden rounded-lg border border-line">
                  {items.map((d, i) => (
                    <li key={i} className="flex flex-wrap items-baseline gap-x-3 gap-y-1 px-3 py-2 text-[13px]">
                      <span className={clsx('rounded px-1.5 py-0.5 text-[11px] font-semibold', KIND[d.kind].cls)}>{KIND[d.kind].label}</span>
                      <span className="font-mono font-semibold">{d.subject}</span>
                      {d.field && <span className="text-muted">{d.field}</span>}
                      <span className="ml-auto text-muted">expected <strong className="font-mono text-fg">{d.expected || '—'}</strong>, found <strong className="font-mono text-fg">{d.actual || '—'}</strong></span>
                    </li>
                  ))}
                </ul>
              </div>
            ))}
          </div>
        )}
      </div>
    </section>
  )
}
