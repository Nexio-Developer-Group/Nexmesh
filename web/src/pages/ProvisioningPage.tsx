import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from '@/api/client'
import { useWorkspace, useWorkspaceEvents } from '@/api/hooks'
import type { Operation } from '@/api/types'
import { Button } from '@/components/ui/Button'
import { TopBar } from '@/layouts/TopBar'

const STEPS = [
  { key: 'allocating', label: 'Allocating', detail: 'Reserving CPU, RAM and storage' },
  { key: 'preparing', label: 'Preparing environment', detail: 'Creating the network and volume, pulling the Ubuntu 24.04 image' },
  { key: 'starting', label: 'Starting', detail: 'Starting the workspace container' },
  { key: 'ready', label: 'Ready', detail: 'Workspace is running' },
]

/** Real provisioning states. The canvas opens only when the final step succeeds (PRD 6.3). */
export default function ProvisioningPage() {
  const { id } = useParams()
  const nav = useNavigate()
  const { data: ws, error } = useWorkspace(id)
  useWorkspaceEvents(id)
  const [showLogs, setShowLogs] = useState(false)
  const ops = useQuery({
    queryKey: ['operations', id],
    queryFn: async () => (await api.get<{ operations: Operation[] }>(`/api/workspaces/${id}/operations`)).operations,
    enabled: !!id && (showLogs || ws?.status === 'failed'),
    refetchInterval: showLogs ? 2000 : false,
  })

  useEffect(() => {
    if (ws?.status === 'ready') {
      const t = setTimeout(() => nav(`/w/${ws.id}`, { replace: true }), 600)
      return () => clearTimeout(t)
    }
  }, [ws?.status, ws?.id, nav])

  if (error) return <p role="alert" className="theme-light p-8">Workspace not found. <Link className="text-blue underline" to="/workspaces">Back to workspaces</Link></p>
  const failed = ws?.status === 'failed'
  const current = Math.max(0, STEPS.findIndex((s) => s.key === ws?.step))

  return (
    <div className="theme-light flex min-h-full flex-col">
      <TopBar crumbs={[{ label: 'Workspaces', to: '/workspaces' }, { label: ws?.name ?? '…' }, { label: 'Provisioning' }]} />
      <main className="mx-auto w-full max-w-xl flex-1 px-6 py-14">
        <h1 className="text-3xl font-extrabold tracking-tight">{failed ? 'Provisioning failed' : ws?.status === 'ready' ? 'Workspace ready' : 'Setting up your code space'}</h1>
        <p className="mt-2 text-muted">{failed ? 'The workspace could not be created. Nothing was left running.' : 'This usually takes under a minute. You can leave this page; provisioning continues.'}</p>

        <ol className="mt-8 space-y-3" aria-label="Provisioning steps">
          {STEPS.map((s, i) => {
            const done = ws?.status === 'ready' || i < current
            const active = !failed && i === current && ws?.status !== 'ready'
            const bad = failed && i === current
            return (
              <li key={s.key} aria-current={active ? 'step' : undefined} className="flex gap-4 rounded-xl border border-line bg-panel p-4">
                <span aria-hidden className={`mt-0.5 grid size-6 shrink-0 place-items-center rounded-full text-xs font-bold ${bad ? 'bg-err-bg text-err-fg' : done ? 'bg-ok-bg text-ok-fg' : active ? 'bg-info-bg text-info-fg' : 'bg-neutral-bg text-faint'}`}>
                  {bad ? '✕' : done ? '✓' : active ? <span className="size-3 rounded-full border-2 border-current border-t-transparent" style={{ animation: 'nx-spin .8s linear infinite' }} /> : i + 1}
                </span>
                <div className="flex-1">
                  <p className="font-semibold">{s.label} <span className="sr-only">{bad ? 'failed' : done ? 'done' : active ? 'in progress' : 'waiting'}</span></p>
                  <p className="text-sm text-muted">{bad ? ws?.error : s.detail}</p>
                </div>
              </li>
            )
          })}
        </ol>

        <div className="mt-6 flex items-center gap-3">
          <Button onClick={() => setShowLogs((v) => !v)}>{showLogs ? 'Hide logs' : 'View logs'}</Button>
          {failed && <Link to="/new"><Button variant="primary">Try different resources</Button></Link>}
          <Link to="/workspaces" className="ml-auto text-sm text-blue hover:underline">Back to workspaces</Link>
        </div>
        {(showLogs || failed) && ops.data && (
          <pre aria-label="Provisioning log" className="mt-4 max-h-64 overflow-auto rounded-xl bg-night p-4 font-mono text-xs leading-relaxed text-night-text">
            {ops.data.filter((o) => o.type === 'provision').flatMap((o) => [...o.log, ...(o.error ? [`error: ${o.error}`] : [])]).join('\n') || 'No log output yet.'}
          </pre>
        )}
      </main>
    </div>
  )
}
