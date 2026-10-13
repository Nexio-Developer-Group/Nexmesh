import clsx from 'clsx'
import { useEffect, useMemo, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { useWorkspaceAction, useWorkspaces } from '@/api/hooks'
import type { Workspace } from '@/api/types'
import { Button } from '@/components/ui/Button'
import { Modal } from '@/components/ui/Modal'
import { Logo } from '@/components/ui/Logo'
import { UserMenu } from '@/layouts/TopBar'
import { DeleteWorkspaceDialog } from '@/features/workspaces/DeleteWorkspaceDialog'
import { isProvisioning, WorkspaceCard } from '@/features/workspaces/WorkspaceCard'

type Filter = 'all' | 'running' | 'stopped' | 'provisioning' | 'failed'
type Sort = 'opened' | 'name' | 'created'

const matchesFilter = (w: Workspace, f: Filter) =>
  f === 'all' ||
  (f === 'running' && w.status === 'ready') ||
  (f === 'stopped' && w.status === 'stopped') ||
  (f === 'provisioning' && isProvisioning(w)) ||
  (f === 'failed' && w.status === 'failed')

export default function DashboardPage() {
  const { data, isLoading, error, refetch } = useWorkspaces()
  const start = useWorkspaceAction('start')
  const stop = useWorkspaceAction('stop')
  const [filter, setFilter] = useState<Filter>('all')
  const [sort, setSort] = useState<Sort>('opened')
  const [view, setView] = useState<'grid' | 'list'>(() => (localStorage.getItem('nx-view') as 'grid' | 'list') || 'grid')
  const [q, setQ] = useState('')
  const [toDelete, setToDelete] = useState<Workspace | null>(null)
  const [errorOf, setErrorOf] = useState<Workspace | null>(null)
  const search = useRef<HTMLInputElement>(null)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === '/' && !(e.target instanceof HTMLInputElement) && !(e.target instanceof HTMLTextAreaElement)) {
        e.preventDefault()
        search.current?.focus()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const list = useMemo(() => {
    const items = (data ?? []).filter((w) => matchesFilter(w, filter) && w.name.toLowerCase().includes(q.trim().toLowerCase()))
    return items.sort((a, b) =>
      sort === 'name' ? a.name.localeCompare(b.name)
      : sort === 'created' ? b.createdAt.localeCompare(a.createdAt)
      : b.lastOpenedAt.localeCompare(a.lastOpenedAt))
  }, [data, filter, sort, q])

  const count = (f: Filter) => (data ?? []).filter((w) => matchesFilter(w, f)).length
  const tabs: [Filter, string][] = [['all', 'All'], ['running', 'Running'], ['stopped', 'Stopped'], ['provisioning', 'Provisioning'], ['failed', 'Failed']]

  return (
    <div className="theme-light flex min-h-full flex-col">
      <header className="flex h-[60px] items-center gap-6 border-b border-line bg-panel px-6">
        <Link to="/" aria-label="Nexmesh home"><Logo /></Link>
        <div className="mx-auto w-full max-w-md">
          <label className="sr-only" htmlFor="ws-search">Search workspaces</label>
          <div className="relative">
            <input ref={search} id="ws-search" value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search workspaces"
              className="h-10 w-full rounded-[10px] border border-line-strong bg-bg px-4 pr-10 text-sm placeholder:text-faint" />
            <kbd className="absolute right-2 top-1/2 -translate-y-1/2 rounded border border-line-strong px-1.5 font-mono text-[11px] text-faint">/</kbd>
          </div>
        </div>
        <Link to="/new"><Button variant="primary">Create Code Space</Button></Link>
        <UserMenu />
      </header>

      <main className="mx-auto w-full max-w-[1400px] flex-1 px-8 py-8">
        <div className="flex flex-wrap items-center gap-4">
          <h1 className="mr-2 text-[32px] font-extrabold tracking-tight">Workspaces</h1>
          <div role="tablist" aria-label="Filter by status" className="flex rounded-[10px] border border-line-strong bg-panel p-1">
            {tabs.map(([k, label]) => (
              <button key={k} role="tab" aria-selected={filter === k} onClick={() => setFilter(k)}
                className={clsx('rounded-md px-3 py-1.5 text-[13px] font-semibold', filter === k ? 'bg-ink text-white' : 'text-muted hover:text-fg')}>
                {label} <span className={clsx('ml-1', filter === k ? 'text-white/70' : 'text-faint')}>{count(k)}</span>
              </button>
            ))}
          </div>
          <div className="ml-auto flex items-center gap-3">
            <label className="flex items-center gap-2 rounded-[10px] border border-line-strong bg-panel px-3 py-1.5 text-[13px] text-muted">
              Sort
              <select value={sort} onChange={(e) => setSort(e.target.value as Sort)} className="bg-transparent font-semibold text-fg">
                <option value="opened">Last opened</option>
                <option value="name">Name</option>
                <option value="created">Created</option>
              </select>
            </label>
            <div role="group" aria-label="Layout" className="flex rounded-[10px] border border-line-strong bg-panel p-1">
              {(['grid', 'list'] as const).map((v) => (
                <button key={v} aria-pressed={view === v} onClick={() => { setView(v); localStorage.setItem('nx-view', v) }}
                  className={clsx('rounded-md px-3 py-1 text-[13px] font-semibold capitalize', view === v ? 'bg-neutral-bg text-fg' : 'text-muted')}>{v}</button>
              ))}
            </div>
          </div>
        </div>

        <div className="mt-6">
          {isLoading ? (
            <div aria-busy="true" aria-label="Loading workspaces" className="grid gap-5 md:grid-cols-2 xl:grid-cols-3">
              {[0, 1, 2].map((i) => <div key={i} className="h-52 rounded-card border border-line bg-panel" style={{ animation: 'nx-pulse 1.4s ease-in-out infinite' }} />)}
            </div>
          ) : error ? (
            <div role="alert" className="rounded-card border border-err-fg/40 bg-err-bg p-6 text-err-fg">
              <p className="font-semibold">Could not load workspaces.</p>
              <p className="mt-1 text-sm">{error.message}</p>
              <Button className="mt-4" onClick={() => refetch()}>Try again</Button>
            </div>
          ) : (data?.length ?? 0) === 0 ? (
            <EmptyState />
          ) : list.length === 0 ? (
            <p className="py-16 text-center text-muted">No workspaces match this filter.</p>
          ) : (
            <div className={clsx(view === 'grid' ? 'grid gap-5 md:grid-cols-2 xl:grid-cols-3' : 'flex flex-col gap-3')}>
              {list.map((w) => (
                <WorkspaceCard key={w.id} workspace={w} view={view}
                  onStart={(x) => start.mutate(x.id)} onStop={(x) => stop.mutate(x.id)}
                  onDelete={setToDelete} onViewError={setErrorOf} />
              ))}
            </div>
          )}
        </div>
      </main>

      {toDelete && <DeleteWorkspaceDialog workspace={toDelete} onClose={() => setToDelete(null)} />}
      {errorOf && (
        <Modal title={`${errorOf.name} failed to provision`} onClose={() => setErrorOf(null)}
          footer={<><Button onClick={() => setErrorOf(null)}>Close</Button><Button variant="danger" onClick={() => { setToDelete(errorOf); setErrorOf(null) }}>Delete workspace…</Button></>}>
          <p className="rounded-lg bg-err-bg p-3 font-mono text-[12px] text-err-fg">{errorOf.error}</p>
          <p className="mt-3">Fix the cause (for example choose fewer resources), then create the workspace again.</p>
        </Modal>
      )}
    </div>
  )
}

/** Empty state built from the isometric block motif: one clear action. */
function EmptyState() {
  return (
    <div className="grid place-items-center rounded-card border border-dashed border-line-strong bg-panel px-6 py-20 text-center">
      <svg width="96" height="96" viewBox="0 0 96 96" aria-hidden>
        <polygon points="48,14 76,30 76,62 48,78 20,62 20,30" fill="none" stroke="#1676FE" strokeWidth="2.5" />
        <polyline points="20,30 48,46 76,30" fill="none" stroke="#1676FE" strokeWidth="2.5" />
        <line x1="48" y1="46" x2="48" y2="78" stroke="#1676FE" strokeWidth="2.5" />
        <polygon points="74,10 82,14.5 82,23.5 74,28 66,23.5 66,14.5" fill="#00B8FF" opacity=".35" />
      </svg>
      <h2 className="mt-5 text-xl font-extrabold">No workspaces yet</h2>
      <p className="mt-1 max-w-sm text-muted">Create a code space, then drag technologies onto its canvas.</p>
      <Link to="/new" className="mt-6"><Button variant="primary" size="lg">Create Code Space</Button></Link>
    </div>
  )
}
