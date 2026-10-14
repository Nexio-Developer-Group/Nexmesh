import clsx from 'clsx'
import { useState } from 'react'
import { Link, Navigate, useParams, useSearchParams } from 'react-router-dom'
import { useWorkspace } from '@/api/hooks'
import { Logo } from '@/components/ui/Logo'
import { UserMenu } from '@/layouts/TopBar'
import { CodeAgentTab } from '@/features/settings/CodeAgentTab'
import { GeneralTab } from '@/features/settings/GeneralTab'
import { VariablesTab } from '@/features/variables/VariablesTab'
import { ResourcesTab } from '@/features/settings/ResourcesTab'

const TABS = [['general', 'General'], ['resources', 'Resources'], ['variables', 'Variables'], ['agent', 'Code Agent']] as const
type Tab = (typeof TABS)[number][0]

export default function SettingsPage() {
  const { id } = useParams()
  const { data: ws, error } = useWorkspace(id)
  const [params, setParams] = useSearchParams()
  const [tab, setTabState] = useState<Tab>((params.get('tab') as Tab) || 'general')
  const setTab = (t: Tab) => { setTabState(t); setParams({ tab: t }, { replace: true }) }
  if (error) return <Navigate to="/workspaces" replace />

  return (
    <div className="theme-dark flex h-full flex-col">
      <header className="flex h-[60px] shrink-0 items-center gap-4 border-b border-line bg-panel px-6">
        <Link to="/workspaces" aria-label="Nexmesh home"><Logo /></Link>
        <nav aria-label="Breadcrumb" className="flex items-center gap-2 border-l border-line-strong pl-4 text-[13px]">
          <Link to="/workspaces" className="text-muted hover:text-fg">Workspaces</Link><span className="text-faint">/</span>
          <Link to={`/w/${id}`} className="text-muted hover:text-fg">{ws?.name ?? '…'}</Link><span className="text-faint">/</span>
          <span className="font-semibold">Settings</span>
        </nav>
        <div className="ml-auto"><UserMenu /></div>
      </header>
      <div className="flex min-h-0 flex-1">
        <nav aria-label="Workspace settings" className="w-[260px] shrink-0 border-r border-line bg-panel p-4">
          <p className="mb-3 px-2 font-mono text-[10.5px] font-semibold uppercase tracking-wider text-faint">Workspace settings</p>
          <ul className="space-y-1">
            {TABS.map(([k, label]) => (
              <li key={k}>
                <button aria-current={tab === k ? 'page' : undefined} onClick={() => setTab(k)}
                  className={clsx('w-full rounded-[10px] px-3 py-2 text-left text-sm font-medium', tab === k ? 'border-l-2 border-blue bg-raised text-fg' : 'text-muted hover:text-fg')}>{label}</button>
              </li>
            ))}
          </ul>
        </nav>
        <main className="min-w-0 flex-1 overflow-y-auto bg-bg p-8">
          {!ws ? <p className="text-muted">Loading…</p> : tab === 'general' ? <GeneralTab key={`${ws.name}|${ws.description}`} workspace={ws} /> : tab === 'resources' ? <ResourcesTab workspace={ws} /> : tab === 'variables' ? <VariablesTab workspaceId={ws.id} /> : <CodeAgentTab workspaceId={ws.id} />}
        </main>
      </div>
    </div>
  )
}
