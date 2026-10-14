import { useState } from 'react'
import type { CanvasComponent, CatalogComponent, Wire } from '@/api/types'
import { Button } from '@/components/ui/Button'
import { canConnect } from './rules'
import { WireStateBadge } from './WireDialog'

interface Props {
  component: CanvasComponent
  def: CatalogComponent
  components: CanvasComponent[]
  defs: Map<string, CatalogComponent>
  wires: Wire[]
  onNew: (from: CanvasComponent, to: CanvasComponent) => void
  onOpen: (w: Wire) => void
}

const label = (c: CanvasComponent, d?: CatalogComponent) => (c.connName ? `${d?.name ?? c.catalogId} (${c.connName})` : d?.name ?? c.catalogId)

/**
 * Connections for the selected card (PRD F1). This is the keyboard and screen
 * reader route to everything the drag-to-connect gesture does on the canvas.
 */
export function ConnectionsSection({ component: c, def, components, defs, wires, onNew, onOpen }: Props) {
  const [pick, setPick] = useState('')
  const out = wires.filter((w) => w.fromId === c.id)
  const inc = wires.filter((w) => w.toId === c.id)
  const consumes = !!def.wiring?.consumes?.length
  const candidates = components.filter((o) => o.id !== c.id && canConnect(def, defs.get(o.catalogId)).ok && !out.some((w) => w.toId === o.id))
  if (!consumes && inc.length === 0 && !def.wiring?.provides) return null
  const byId = (id: string) => components.find((x) => x.id === id)

  return (
    <section aria-label="Connections" className="border-t border-line px-5 py-4">
      <h3 className="text-[13px] font-bold">Connections</h3>
      {consumes && (
        <>
          {out.length === 0 && <p className="mt-1.5 text-[13px] text-muted">Not connected to anything yet. Drag from this card's right handle, or choose a card below.</p>}
          <ul className="mt-2 space-y-2">
            {out.map((w) => {
              const to = byId(w.toId)
              return (
                <li key={w.id}>
                  <button onClick={() => onOpen(w)} className="flex w-full items-center justify-between gap-2 rounded-[10px] border border-line-strong bg-raised px-3 py-2 text-left text-[13px] hover:border-blue">
                    <span className="min-w-0 truncate">→ {to ? label(to, defs.get(to.catalogId)) : 'removed card'} <span className="font-mono text-[11px] text-muted">{w.name}</span></span>
                    <WireStateBadge state={w.state} />
                  </button>
                </li>
              )
            })}
          </ul>
          <div className="mt-3 flex items-center gap-2">
            <label className="sr-only" htmlFor="wire-target">Connect to</label>
            <select id="wire-target" value={pick} onChange={(e) => setPick(e.target.value)} disabled={candidates.length === 0}
              className="h-9 min-w-0 flex-1 rounded-[10px] border border-line-strong bg-field px-2 text-[13px] text-fg">
              <option value="">{candidates.length ? 'Connect to…' : 'No compatible cards on the canvas'}</option>
              {candidates.map((o) => <option key={o.id} value={o.id}>{label(o, defs.get(o.catalogId))}</option>)}
            </select>
            <Button size="sm" disabled={!pick} onClick={() => { const to = byId(pick); if (to) { onNew(c, to); setPick('') } }}>Review…</Button>
          </div>
        </>
      )}
      {inc.length > 0 && (
        <div className="mt-3">
          <p className="text-[13px] text-muted">Used by</p>
          <ul className="mt-1 space-y-1 text-[13px]">
            {inc.map((w) => {
              const from = byId(w.fromId)
              return <li key={w.id} className="flex items-center justify-between gap-2"><span className="truncate">{from ? label(from, defs.get(from.catalogId)) : 'removed card'}</span><WireStateBadge state={w.state} /></li>
            })}
          </ul>
        </div>
      )}
    </section>
  )
}
