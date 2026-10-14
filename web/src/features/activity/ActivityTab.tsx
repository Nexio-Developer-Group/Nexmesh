import { useState } from 'react'
import { ApiError } from '@/api/client'
import { Button } from '@/components/ui/Button'
import { auditParams, emptyFilters, useAudit, type AuditEvent, type AuditFilters } from './hooks'

const CATEGORIES: [string, string][] = [
  ['', 'All activity'], ['workspace.', 'Workspace and files'], ['component.', 'Components'], ['connection.', 'Connections'], ['variable.', 'Variables'],
  ['git.', 'Git'], ['share.', 'Sharing'], ['port.', 'Ports'], ['agent.', 'Code agent'], ['audit.', 'Exports'],
]
const ACTORS: [string, string][] = [['', 'Anyone'], ['user', 'People'], ['agent', 'Agents'], ['system', 'System']]

const ACTOR_LABEL: Record<string, string> = { user: 'Person', agent: 'Agent', system: 'System' }

export function ActivityTab({ workspaceId }: { workspaceId: string }) {
  const [draft, setDraft] = useState<AuditFilters>(emptyFilters)
  const [applied, setApplied] = useState<AuditFilters>(emptyFilters)
  const q = useAudit(workspaceId, applied)
  const events = q.data?.pages.flatMap((p) => p.events) ?? []
  const set = (k: keyof AuditFilters) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setDraft((d) => ({ ...d, [k]: e.target.value }))
  const exportHref = (format: 'csv' | 'json') => `/api/workspaces/${workspaceId}/audit/export?${auditParams(applied, { format })}`
  const dirty = JSON.stringify(draft) !== JSON.stringify(applied)
  const input = 'h-9 rounded-lg border border-line-strong bg-field px-2.5 text-[13px]'

  return (
    <section aria-label="Activity" className="max-w-5xl space-y-5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-2xl font-extrabold tracking-tight">Activity</h2>
          <p className="mt-1 max-w-xl text-muted">Every change made to this workspace, by people, agents and Nexmesh itself. Values of secrets are never recorded.</p>
        </div>
        <div className="flex gap-2">
          <a className="inline-flex h-8 items-center rounded-[10px] border border-line-strong bg-raised px-3 text-xs font-semibold hover:border-blue" href={exportHref('csv')} download>Export CSV</a>
          <a className="inline-flex h-8 items-center rounded-[10px] border border-line-strong bg-raised px-3 text-xs font-semibold hover:border-blue" href={exportHref('json')} download>Export JSON</a>
        </div>
      </div>

      <form aria-label="Filter activity" className="flex flex-wrap items-end gap-3" onSubmit={(e) => { e.preventDefault(); setApplied(draft) }}>
        <label className="text-xs font-semibold">Category
          <select className={`${input} mt-1 block`} value={draft.action} onChange={set('action')}>{CATEGORIES.map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select>
        </label>
        <label className="text-xs font-semibold">Who
          <select className={`${input} mt-1 block`} value={draft.actor} onChange={set('actor')}>{ACTORS.map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select>
        </label>
        <label className="text-xs font-semibold">From
          <input type="date" className={`${input} mt-1 block`} value={draft.from} onChange={set('from')} />
        </label>
        <label className="text-xs font-semibold">To
          <input type="date" className={`${input} mt-1 block`} value={draft.to} onChange={set('to')} />
        </label>
        <label className="min-w-[180px] flex-1 text-xs font-semibold">Search
          <input type="search" className={`${input} mt-1 block w-full`} placeholder="Action, target or person" value={draft.q} onChange={set('q')} />
        </label>
        <Button type="submit" size="sm" variant="primary" disabled={!dirty}>Apply</Button>
        {JSON.stringify(applied) !== JSON.stringify(emptyFilters) && <Button type="button" size="sm" variant="ghost" onClick={() => { setDraft(emptyFilters); setApplied(emptyFilters) }}>Clear</Button>}
      </form>

      {q.error instanceof ApiError && <p role="alert" className="rounded-lg bg-err-bg p-3 text-sm text-err-fg">{q.error.message}</p>}
      {q.isPending && <p className="text-muted">Loading…</p>}
      {q.data && events.length === 0 && <p className="rounded-card border border-dashed border-line-strong p-8 text-center text-sm text-muted">No activity matches these filters.</p>}
      {events.length > 0 && (
        <div className="overflow-hidden rounded-card border border-line bg-panel">
          <table className="w-full text-left text-[13px]">
            <caption className="sr-only">Workspace activity, newest first</caption>
            <thead className="border-b border-line text-[11px] uppercase tracking-wider text-faint">
              <tr><th scope="col" className="px-4 py-2.5">When</th><th scope="col" className="px-4 py-2.5">Who</th><th scope="col" className="px-4 py-2.5">What</th><th scope="col" className="px-4 py-2.5">Details</th></tr>
            </thead>
            <tbody>{events.map((e) => <Row key={e.id} e={e} />)}</tbody>
          </table>
        </div>
      )}
      {q.hasNextPage && <Button loading={q.isFetchingNextPage} onClick={() => q.fetchNextPage()}>Load more</Button>}
      {q.data?.pages[0]?.truncated && <p className="text-xs text-faint">Showing the most recent 10,000 events.</p>}
    </section>
  )
}

function Row({ e }: { e: AuditEvent }) {
  const [open, setOpen] = useState(false)
  const hasDetail = !!e.detail && Object.keys(e.detail).length > 0
  const when = new Date(e.at)
  return (
    <tr className="border-b border-line align-top last:border-0">
      <td className="whitespace-nowrap px-4 py-2.5 text-muted"><time dateTime={e.at} title={e.at}>{when.toLocaleString()}</time></td>
      <td className="px-4 py-2.5"><span className="rounded bg-neutral-bg px-1.5 py-0.5 text-[11px] font-semibold text-muted">{ACTOR_LABEL[e.actorType] ?? e.actorType}</span>{e.actorId && <span className="ml-1.5 font-mono text-[11px] text-faint">{e.actorId}</span>}</td>
      <td className="px-4 py-2.5"><span className="font-mono text-[12.5px] font-semibold">{e.action}</span>{e.target && <span className="ml-2 text-muted">{e.target}</span>}</td>
      <td className="px-4 py-2.5">
        {hasDetail ? (
          <>
            <button type="button" aria-expanded={open} onClick={() => setOpen((o) => !o)} className="text-xs font-semibold text-link hover:underline">{open ? 'Hide' : 'Show'}<span className="sr-only"> details for {e.action}</span></button>
            {open && <pre className="mt-1 max-w-sm overflow-x-auto whitespace-pre-wrap rounded bg-bg p-2 font-mono text-[11px]">{JSON.stringify(e.detail, null, 2)}</pre>}
          </>
        ) : <span className="text-faint">—</span>}
      </td>
    </tr>
  )
}
