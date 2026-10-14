import { useCallback, useMemo, useState } from 'react'
import {
  Background, BackgroundVariant, Panel, ReactFlow, ReactFlowProvider, useReactFlow, useViewport,
  MarkerType, type Connection, type Edge, type NodeChange, type NodeTypes,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import type { CanvasComponent, CatalogComponent, Wire } from '@/api/types'
import { canConnect } from '@/features/wires/rules'
import { DRAG_MIME } from './CatalogSidebar'
import { TechNode, type TechNodeType } from './TechNode'

const nodeTypes: NodeTypes = { tech: TechNode }

interface Props {
  components: CanvasComponent[]
  catalog: Map<string, CatalogComponent>
  selectedId: string | null
  onSelect: (id: string | null) => void
  onDropComponent: (catalogId: string, x: number, y: number) => void
  onMove: (id: string, x: number, y: number) => void
  onRetry: (c: CanvasComponent) => void
  onViewLogs: (c: CanvasComponent) => void
  wires?: Wire[]
  /** The user dragged a line from a consumer to a provider (nothing is created yet). */
  onConnect?: (fromId: string, toId: string) => void
  onSelectWire?: (w: Wire) => void
}

const WIRE_STYLE: Record<string, { stroke: string; dash?: string }> = {
  healthy: { stroke: '#2fbf71' },
  degraded: { stroke: '#e0a526', dash: '6 4' },
  unreachable: { stroke: '#e5484d', dash: '2 5' },
  pending: { stroke: '#7a86a8', dash: '4 4' },
}
const WIRE_WORD: Record<string, string> = { healthy: 'healthy', degraded: 'degraded', unreachable: 'unreachable', pending: 'waiting' }

function Zoom() {
  const rf = useReactFlow()
  const { zoom } = useViewport()
  const btn = 'grid h-8 w-8 place-items-center text-muted hover:text-fg'
  return (
    <Panel position="bottom-left">
      <div className="flex items-center rounded-[10px] border border-line-strong bg-panel text-xs shadow-lg">
        <button className={btn} aria-label="Zoom out" onClick={() => rf.zoomOut()}>−</button>
        <span className="w-12 text-center font-mono" aria-live="polite">{Math.round(zoom * 100)}%</span>
        <button className={btn} aria-label="Zoom in" onClick={() => rf.zoomIn()}>+</button>
        <span className="mx-1 h-4 w-px bg-line-strong" />
        <button className="px-3 text-muted hover:text-fg" onClick={() => rf.fitView({ padding: 0.3, maxZoom: 1 })}>Fit</button>
      </div>
    </Panel>
  )
}

function Inner({ components, catalog, selectedId, onSelect, onDropComponent, onMove, onRetry, onViewLogs, wires = [], onConnect, onSelectWire }: Props) {
  const rf = useReactFlow()
  const [pos, setPos] = useState<Record<string, { x: number; y: number }>>({})
  const [dragOver, setDragOver] = useState(false)

  const nodes = useMemo<TechNodeType[]>(
    () =>
      components.flatMap((c) => {
        const def = catalog.get(c.catalogId)
        if (!def) return []
        return [{
          id: c.id, type: 'tech' as const, position: pos[c.id] ?? { x: c.x, y: c.y }, selected: c.id === selectedId,
          data: { component: c, def, selected: c.id === selectedId, onRetry, onViewLogs },
        }]
      }),
    [components, catalog, pos, selectedId, onRetry, onViewLogs],
  )

  // A line's colour comes with a word and a dash pattern, never colour alone (PRD 19.4).
  const edges = useMemo<Edge[]>(
    () =>
      wires.map((w) => {
        const st = WIRE_STYLE[w.state] ?? WIRE_STYLE.pending
        return {
          id: w.id, source: w.fromId, target: w.toId, type: 'smoothstep',
          label: `${w.name} · ${WIRE_WORD[w.state] ?? w.state}`,
          labelStyle: { fill: '#c9d3ee', fontSize: 11, fontFamily: 'JetBrains Mono, monospace' },
          labelBgStyle: { fill: '#0e1730' }, labelBgPadding: [6, 3] as [number, number], labelBgBorderRadius: 6,
          style: { stroke: st.stroke, strokeWidth: 2, strokeDasharray: st.dash },
          markerEnd: { type: MarkerType.ArrowClosed, color: st.stroke },
          ariaLabel: `Connection ${w.name}, ${w.state}`,
          data: { wire: w },
        }
      }),
    [wires],
  )

  const isValidConnection = useCallback(
    (c: Connection | Edge) => {
      const from = components.find((x) => x.id === c.source)
      const to = components.find((x) => x.id === c.target)
      if (!from || !to || from.id === to.id) return false
      if (wires.some((w) => w.fromId === from.id && w.toId === to.id)) return false
      return canConnect(catalog.get(from.catalogId), catalog.get(to.catalogId)).ok
    },
    [components, catalog, wires],
  )

  const onNodesChange = useCallback((changes: NodeChange[]) => {
    setPos((p) => {
      let next = p
      for (const ch of changes) if (ch.type === 'position' && ch.position) next = { ...next, [ch.id]: ch.position }
      return next
    })
  }, [])

  const onDrop = (e: React.DragEvent) => {
    e.preventDefault()
    setDragOver(false)
    const id = e.dataTransfer.getData(DRAG_MIME)
    if (!id) return
    const p = rf.screenToFlowPosition({ x: e.clientX, y: e.clientY })
    onDropComponent(id, Math.round(p.x - 108), Math.round(p.y - 40))
  }

  return (
    <div className="relative h-full" onDragOver={(e) => { if (e.dataTransfer.types.includes(DRAG_MIME)) { e.preventDefault(); e.dataTransfer.dropEffect = 'copy'; setDragOver(true) } }}
      onDragLeave={() => setDragOver(false)} onDrop={onDrop}>
      <ReactFlow
        nodes={nodes}
        edges={edges}
        isValidConnection={isValidConnection}
        onConnect={(c) => c.source && c.target && onConnect?.(c.source, c.target)}
        onEdgeClick={(_, e) => { const w = (e.data as { wire?: Wire } | undefined)?.wire; if (w) onSelectWire?.(w) }}
        nodeTypes={nodeTypes}
        onNodesChange={onNodesChange}
        onNodeClick={(_, n) => onSelect(n.id)}
        onPaneClick={() => onSelect(null)}
        onNodeDragStop={(_, n) => onMove(n.id, Math.round(n.position.x), Math.round(n.position.y))}
        nodesConnectable={!!onConnect}
        minZoom={0.3}
        maxZoom={1.6}
        fitView
        fitViewOptions={{ padding: 0.3, maxZoom: 1 }}
        proOptions={{ hideAttribution: true }}
        colorMode="dark"
        aria-label="Workspace canvas"
      >
        <Background variant={BackgroundVariant.Dots} gap={22} size={1.2} color="#2a3a66" />
        <Zoom />
      </ReactFlow>
      {components.length === 0 && (
        <p className="pointer-events-none absolute inset-0 grid place-items-center text-sm text-faint">Drag a technology here</p>
      )}
      {dragOver && <div aria-hidden className="pointer-events-none absolute inset-3 rounded-2xl border-2 border-dashed border-blue/70 bg-blue/5" />}
    </div>
  )
}

export function Canvas(props: Props) {
  return (
    <ReactFlowProvider>
      <Inner {...props} />
    </ReactFlowProvider>
  )
}
