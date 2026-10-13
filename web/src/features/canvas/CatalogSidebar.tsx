import clsx from 'clsx'
import { useMemo, useRef, useState } from 'react'
import type { Catalog, CanvasComponent } from '@/api/types'
import { Monogram } from './TechNode'

export const DRAG_MIME = 'application/x-nexmesh-component'

interface Props {
  catalog: Catalog
  onCanvas: CanvasComponent[]
  onAdd: (catalogId: string) => void
  runtimes: { name: string; id: string; active?: string; installed: string[] }[]
  onInstallRuntime?: () => void
  disabled?: boolean
}

/** Searchable, categorized technology catalog. Items drag onto the canvas, or are added with Enter for keyboard users. */
export function CatalogSidebar({ catalog, onCanvas, onAdd, runtimes, onInstallRuntime, disabled }: Props) {
  const [q, setQ] = useState('')
  const [open, setOpen] = useState<Record<string, boolean>>({ database: true, backend: true, frontend: true, messaging: true })
  const search = useRef<HTMLInputElement>(null)
  const byId = useMemo(() => new Map(catalog.components.map((c) => [c.id, c])), [catalog])
  const onCanvasIds = new Set(onCanvas.map((c) => c.catalogId))
  const query = q.trim().toLowerCase()

  return (
    <aside aria-label="Technology catalog" className="flex w-[260px] shrink-0 flex-col border-r border-line bg-panel">
      <div className="p-3">
        <div className="relative">
          <label htmlFor="catalog-search" className="sr-only">Search technologies</label>
          <input id="catalog-search" ref={search} value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search technologies"
            className="h-10 w-full rounded-[10px] border border-line-strong bg-field px-3 pr-10 text-sm placeholder:text-faint" />
          <kbd className="absolute right-2 top-1/2 -translate-y-1/2 rounded border border-line-strong px-1.5 font-mono text-[10px] text-faint">⌘K</kbd>
        </div>
      </div>
      <nav className="flex-1 overflow-y-auto px-3 pb-3">
        {catalog.categories.map((cat) => {
          const items = cat.items.map((id) => byId.get(id)!).filter((c) => c && (!query || c.name.toLowerCase().includes(query)))
          if (query && items.length === 0) return null
          const expanded = query ? true : (open[cat.id] ?? false)
          return (
            <section key={cat.id} className="mt-3">
              <button aria-expanded={expanded} onClick={() => setOpen((o) => ({ ...o, [cat.id]: !expanded }))}
                className="flex w-full items-center justify-between px-1 py-1.5 text-left font-mono text-[10.5px] font-semibold uppercase tracking-wider text-faint hover:text-muted">
                <span>{cat.label}</span>
                {!expanded && <span>{items.length} ›</span>}
              </button>
              {expanded && (
                <ul className="mt-1">
                  {items.map((c) => (
                    <li key={c.id}>
                      <button
                        draggable={!disabled}
                        disabled={disabled}
                        aria-label={`Add ${c.name} to canvas`}
                        onDragStart={(e) => { e.dataTransfer.setData(DRAG_MIME, c.id); e.dataTransfer.effectAllowed = 'copy' }}
                        onClick={() => onAdd(c.id)}
                        className={clsx('group flex w-full cursor-grab items-center gap-3 rounded-[10px] px-2 py-1.5 text-left text-sm hover:bg-raised active:cursor-grabbing disabled:cursor-not-allowed disabled:opacity-50')}
                      >
                        <Monogram text={c.monogram} size={26} />
                        <span className="flex-1">{c.name}</span>
                        {onCanvasIds.has(c.id) && <span aria-label="on canvas" className="size-1.5 rounded-full bg-info-fg" />}
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </section>
          )
        })}
      </nav>
      <section aria-label="Runtimes" className="border-t border-line p-4">
        <div className="mb-2 flex items-center justify-between">
          <h2 className="font-mono text-[10.5px] font-semibold uppercase tracking-wider text-faint">Runtimes</h2>
          {onInstallRuntime && <button onClick={onInstallRuntime} className="text-xs font-semibold text-info-fg hover:underline">Install version</button>}
        </div>
        <ul className="space-y-1 text-sm">
          {runtimes.map((r) => (
            <li key={r.id} className="flex justify-between">
              <span>{r.name}</span>
              {r.active ? <span className="font-mono text-xs text-ok-fg">{r.active} active</span> : <span className="font-mono text-xs text-faint">not installed</span>}
            </li>
          ))}
        </ul>
      </section>
    </aside>
  )
}
