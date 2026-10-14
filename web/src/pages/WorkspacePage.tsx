import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link, Navigate, useParams, useSearchParams } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { api } from '@/api/client'
import { useCatalog, useWorkspace, useWorkspaceEvents } from '@/api/hooks'
import type { CanvasComponent, Wire } from '@/api/types'
import { Button } from '@/components/ui/Button'
import { Logo } from '@/components/ui/Logo'
import { StatusBadge } from '@/components/ui/StatusBadge'
import { UserMenu } from '@/layouts/TopBar'
import { Canvas } from '@/features/canvas/Canvas'
import { useWires } from '@/features/wires/hooks'
import { ArchitectureView } from '@/features/architecture/ArchitectureView'
import { ConnectionsSection } from '@/features/wires/ConnectionsSection'
import { NewWireDialog, WireDetailsDialog } from '@/features/wires/WireDialog'
import { CatalogSidebar } from '@/features/canvas/CatalogSidebar'
import { PropertiesPanel } from '@/features/canvas/PropertiesPanel'
import { RuntimesPanel } from '@/features/canvas/RuntimesPanel'
import { ConnectTab } from '@/features/connect/ConnectTab'
import { ComponentActivity } from '@/features/health/ComponentActivity'
import { InstallTab } from '@/features/canvas/InstallTab'
import { applyComponentEvent, applyOperationEvent, useAddComponent, useComponents, useMoveComponent } from '@/features/canvas/hooks'
import { osLabel } from '@/lib/format'
import { ImportConnectionDialog } from '@/features/connect/ImportConnectionDialog'
import { usePrefill } from '@/features/connect/prefill'
import { TemplatesDialog } from '@/features/templates/TemplatesDialog'
import { CodeView } from '@/features/code/CodeView'

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
  const [templatesOpen, setTemplatesOpen] = useState(false)
  const [importOpen, setImportOpen] = useState(false)
  const { data: wires = [] } = useWires(id)
  const [newWire, setNewWire] = useState<{ fromId: string; toId: string } | null>(null)
  const [openWire, setOpenWire] = useState<Wire | null>(null)
  const [params, setParams] = useSearchParams()
  const view = params.get('view') === 'code' ? 'code' : params.get('view') === 'architecture' ? 'architecture' : 'canvas'
  const setView = (v: 'canvas' | 'architecture' | 'code') => setParams(v === 'canvas' ? {} : { view: v }, { replace: true })

  useWorkspaceEvents(id, (type, data) => {
    if (!id) return
    applyComponentEvent(qc, id, type, data)
    if (type === 'operation') applyOperationEvent(qc, id, data)
    if (type === 'ports') qc.invalidateQueries({ queryKey: ['ports', id] })
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
        <div role="tablist" aria-label="Workspace views" className="ml-4 flex rounded-[10px] border border-line-strong p-0.5 text-[13px]">
          {(['canvas', 'architecture', 'code'] as const).map((v) => (
            <button key={v} role="tab" aria-selected={view === v} onClick={() => setView(v)}
              className={view === v ? 'rounded-lg bg-raised px-3 py-1 font-semibold text-fg' : 'rounded-lg px-3 py-1 text-muted hover:text-fg'}>{v === 'canvas' ? 'Canvas' : v === 'architecture' ? 'Architecture' : 'Code'}</button>
          ))}
        </div>
        <div className="ml-auto flex items-center gap-3">
          <Button size="sm" onClick={() => setImportOpen(true)} disabled={ws?.status !== 'ready'}>Import connection</Button>
          <Button size="sm" onClick={() => setTemplatesOpen(true)} disabled={ws?.status !== 'ready'}>Templates</Button>
          <Button size="sm" onClick={() => setRuntimesOpen(true)}>Runtimes</Button>
          <Link to={`/w/${id}/settings`}><Button size="sm">Workspace settings</Button></Link>
          <UserMenu />
        </div>
      </header>

      {view === 'code' && ws && <CodeView workspace={ws} />}
      {view === 'architecture' && id && <ArchitectureView workspaceId={id} />}
      {importOpen && <ImportConnectionDialog defs={defs} onClose={() => setImportOpen(false)} onPick={(cand) => {
        // A Draft card is created and the values wait in memory for its Connect form; nothing is saved or tested yet.
        const n = comps.length
        add.mutate({ catalogId: cand.catalogId, x: 40 + (n % 3) * 250, y: 40 + Math.floor(n / 3) * 230 }, { onSuccess: (c) => {
          const def = defs.get(cand.catalogId)
          const plain: Record<string, unknown> = {}
          const sec: Record<string, string> = {}
          for (const f of def?.connection?.fields ?? []) {
            const v = cand.fields[f.name]
            if (v === undefined) continue
            if (f.type === 'password') sec[f.name] = String(v)
            else plain[f.name] = v
          }
          usePrefill.getState().set(c.id, { connName: cand.name, fields: plain, secrets: sec })
          setSelectedId(c.id)
          setImportOpen(false)
        } })
      }} />}
      {newWire && id && comps.find((c) => c.id === newWire.fromId) && comps.find((c) => c.id === newWire.toId) && (
        <NewWireDialog workspaceId={id} from={comps.find((c) => c.id === newWire.fromId)!} to={comps.find((c) => c.id === newWire.toId)!} onClose={() => setNewWire(null)} />
      )}
      {openWire && id && (
        <WireDetailsDialog workspaceId={id} wire={openWire}
          fromName={defs.get(comps.find((c) => c.id === openWire.fromId)?.catalogId ?? '')?.name ?? 'Card'}
          toName={defs.get(comps.find((c) => c.id === openWire.toId)?.catalogId ?? '')?.name ?? 'Card'}
          onClose={() => setOpenWire(null)} />
      )}
      {templatesOpen && id && <TemplatesDialog workspaceId={id} defs={defs} canvasHasCards={comps.length > 0} onClose={() => setTemplatesOpen(false)} />}
      <div className={view === 'canvas' ? 'flex min-h-0 flex-1' : 'hidden'}>
        {catalog && (
          <CatalogSidebar catalog={catalog} onCanvas={comps} onAdd={addNext} runtimes={runtimes} onInstallRuntime={() => setRuntimesOpen(true)} disabled={ws?.status !== 'ready'} />
        )}
        <main className="relative flex min-w-0 flex-1 flex-col bg-bg" aria-label="Canvas">
          <div className="flex items-center gap-3 px-5 py-3 text-[13px]">
            <span className="font-semibold">Canvas</span>
            <span className="text-faint">{comps.length} component{comps.length === 1 ? '' : 's'}{draftCount ? ` · ${draftCount} draft` : ''}</span>
            <span className="ml-auto text-xs text-muted">Drag from a card's right handle to the card it uses to connect them.</span>
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
              wires={wires}
              onConnect={(fromId, toId) => setNewWire({ fromId, toId })}
              onSelectWire={setOpenWire}
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
            renderConnect={(c, def) => <ConnectTab workspaceId={id!} component={c} def={def} />}
            renderConnections={(c, def) => (
              <ConnectionsSection component={c} def={def} components={comps} defs={defs} wires={wires}
                onNew={(from, to) => setNewWire({ fromId: from.id, toId: to.id })} onOpen={setOpenWire} />
            )}
            renderActivity={(c, def) => def.supportedModes.includes('connect') || def.supportedModes.includes('install') && def.kind === 'service'
              ? <ComponentActivity workspaceId={id!} component={c} hasLogs={c.mode === 'installed' && def.kind === 'service'} /> : null}
          />
        )}
      </div>
    </div>
  )
}
