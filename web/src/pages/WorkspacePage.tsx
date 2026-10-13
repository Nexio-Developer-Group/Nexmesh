import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link, Navigate, useParams } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import { useCatalog, useWorkspace, useWorkspaceEvents } from '@/api/hooks'
import type { CanvasComponent } from '@/api/types'
import { Button } from '@/components/ui/Button'
import { Logo } from '@/components/ui/Logo'
import { StatusBadge } from '@/components/ui/StatusBadge'
import { UserMenu } from '@/layouts/TopBar'
import { Canvas } from '@/features/canvas/Canvas'
import { CatalogSidebar } from '@/features/canvas/CatalogSidebar'
import { PropertiesPanel } from '@/features/canvas/PropertiesPanel'
import { RuntimesPanel } from '@/features/canvas/RuntimesPanel'
import { InstallTab } from '@/features/canvas/InstallTab'
import { applyComponentEvent, applyOperationEvent, useAddComponent, useComponents, useMoveComponent } from '@/features/canvas/hooks'
import { osLabel } from '@/lib/format'

export default function WorkspacePage() {
  const { id } = useParams()
  const qc = useQueryClient()
  const { data: ws, error } = useWorkspace(id)
  const { data: catalog } = useCatalog()
  const { data: comps = [] } = useComponents(id)
  const add = useAddComponent(id ?? '')
  const move = useMoveComponent(id ?? '')
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [runtimesOpen, setRuntimesOpen] = useState(false)

  useWorkspaceEvents(id, (type, data) => {
    if (!id) return
    applyComponentEvent(qc, id, type, data)
    if (type === 'operation') applyOperationEvent(qc, id, data)
  })

  const defs = useMemo(() => new Map((catalog?.components ?? []).map((c) => [c.id, c])), [catalog])
  const selected = comps.find((c) => c.id === selectedId)

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && setSelectedId(null)
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const dropAt = useCallback((catalogId: string, x: number, y: number) => {
    add.mutate({ catalogId, x, y }, { onSuccess: (c) => setSelectedId(c.id) })
  }, [add])

  // Keyboard/click alternative to drag: add at the next free grid slot.
  const addNext = (catalogId: string) => {
    const n = comps.length
    dropAt(catalogId, 40 + (n % 3) * 250, 40 + Math.floor(n / 3) * 230)
  }

  if (error) return <Navigate to="/workspaces" replace />
  if (ws && ws.status !== 'ready' && ws.status !== 'stopped') return <Navigate to={`/w/${ws.id}/provisioning`} replace />

  const draftCount = comps.filter((c) => c.observedState === 'draft').length
  const runtimes = ['python', 'nodejs', 'go'].map((rid) => ({
    id: rid, name: defs.get(rid)?.name ?? rid, active: ws?.runtimes?.[rid]?.active, installed: ws?.runtimes?.[rid]?.installed ?? [],
  }))

  return (
    <div className="theme-dark flex h-full flex-col">
      <header className="flex h-[60px] shrink-0 items-center gap-4 border-b border-line bg-panel px-5">
        <Link to="/workspaces" aria-label="Nexmesh home"><Logo /></Link>
        <nav aria-label="Breadcrumb" className="flex items-center gap-2 border-l border-line-strong pl-4 text-[13px]">
          <Link to="/workspaces" className="text-muted hover:text-fg">Workspaces</Link>
          <span className="text-faint">/</span>
          <span className="font-semibold">{ws?.name ?? '…'}</span>
        </nav>
        {ws && <StatusBadge state={ws.status === 'ready' ? 'running' : 'stopped'} />}
        {ws && <span className="hidden font-mono text-[11px] text-muted md:inline">{osLabel(ws.os)} · {ws.cpu} vCPU · {ws.ramGb} GB · {ws.storageGb} GB</span>}
        <div className="ml-auto flex items-center gap-3">
          <Button size="sm" onClick={() => setRuntimesOpen(true)}>Runtimes</Button>
          <Link to={`/w/${id}/settings`}><Button size="sm">Workspace settings</Button></Link>
          <UserMenu />
        </div>
      </header>

      <div className="flex min-h-0 flex-1">
        {catalog && (
          <CatalogSidebar catalog={catalog} onCanvas={comps} onAdd={addNext} runtimes={runtimes} onInstallRuntime={() => setRuntimesOpen(true)} disabled={ws?.status !== 'ready'} />
        )}
        <main className="relative flex min-w-0 flex-1 flex-col bg-bg" aria-label="Canvas">
          <div className="flex items-center gap-3 px-5 py-3 text-[13px]">
            <span className="font-semibold">Canvas</span>
            <span className="text-faint">{comps.length} component{comps.length === 1 ? '' : 's'}{draftCount ? ` · ${draftCount} draft` : ''}</span>
            <span className="ml-auto rounded-full border border-dashed border-line-strong px-3 py-1 text-xs text-muted">
              Connections between components <strong className="ml-1 text-info-fg">Coming soon</strong>
            </span>
          </div>
          <div className="min-h-0 flex-1">
            <Canvas
              components={comps}
              catalog={defs}
              selectedId={selectedId}
              onSelect={setSelectedId}
              onDropComponent={dropAt}
              onMove={(cid, x, y) => move.mutate({ id: cid, x, y })}
              onRetry={(c) => { api.post(`/api/workspaces/${id}/components/${c.id}/retry`).catch(() => setSelectedId(c.id)) }}
              onViewLogs={(c: CanvasComponent) => setSelectedId(c.id)}
            />
          </div>
          {add.error && <p role="alert" className="absolute bottom-4 left-1/2 -translate-x-1/2 rounded-lg bg-err-bg px-4 py-2 text-sm text-err-fg">{add.error.message}</p>}
        </main>
        {runtimesOpen && ws && <RuntimesPanel workspace={ws} defs={defs} onClose={() => setRuntimesOpen(false)} />}
        {selected && defs.get(selected.catalogId) && (
          <PropertiesPanel
            component={selected}
            def={defs.get(selected.catalogId)!}
            onClose={() => setSelectedId(null)}
            renderInstall={(c, def) => <InstallTab workspaceId={id!} component={c} def={def} />}
            renderConnect={(_c, def) => <div className="p-5 text-sm text-muted">Connect to an existing {def.name} deployment.</div>}
          />
        )}
      </div>
    </div>
  )
}
