import clsx from 'clsx'
import { useEffect, useState } from 'react'
import type { CanvasComponent, CatalogComponent } from '@/api/types'
import { ModeBadge, StatusBadge } from '@/components/ui/StatusBadge'
import { Monogram } from './TechNode'

interface Props {
  component: CanvasComponent
  def: CatalogComponent
  onClose: () => void
  renderInstall: (c: CanvasComponent, def: CatalogComponent) => React.ReactNode
  renderConnect: (c: CanvasComponent, def: CatalogComponent) => React.ReactNode
  /** Logs and health history; only offered once a card has a mode. */
  renderActivity?: (c: CanvasComponent, def: CatalogComponent) => React.ReactNode
}

/**
 * Properties panel. Service-type components get Install and Connect tabs
 * (catalog supportedModes); install-only components show no tabs (PRD 11.3).
 */
export function PropertiesPanel({ component: c, def, onClose, renderInstall, renderConnect, renderActivity }: Props) {
  const both = def.supportedModes.includes('install') && def.supportedModes.includes('connect')
  const [tab, setTab] = useState<'install' | 'connect'>(c.mode === 'connected' ? 'connect' : 'install')
  useEffect(() => {
    setTab(c.mode === 'connected' ? 'connect' : 'install')
  }, [c.id]) // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <aside aria-label={`${def.name} properties`} className="flex w-[384px] shrink-0 flex-col border-l border-line bg-panel">
      <div className="p-5 pb-3">
        <div className="flex items-start gap-3">
          <Monogram text={def.monogram} size={40} />
          <div className="min-w-0 flex-1">
            <h2 className="text-lg font-bold leading-tight">{c.connName || def.name}</h2>
            <p className="text-[13px] text-muted">{def.categories[0] ? def.categories[0][0].toUpperCase() + def.categories[0].slice(1) : ''} · {def.kind}</p>
          </div>
          <button aria-label="Close properties" onClick={onClose} className="text-muted hover:text-fg">×</button>
        </div>
        <div className="mt-3 flex items-center gap-2">
          <StatusBadge state={c.observedState} />
          <ModeBadge mode={c.mode} />
        </div>
      </div>
      {both && (
        <div role="tablist" aria-label="Provisioning mode" className="flex border-b border-line px-5">
          {(['install', 'connect'] as const).map((t) => (
            <button key={t} role="tab" aria-selected={tab === t} onClick={() => setTab(t)}
              className={clsx('-mb-px mr-6 border-b-2 pb-2.5 pt-1 text-sm font-semibold capitalize', tab === t ? 'border-blue text-fg' : 'border-transparent text-muted hover:text-fg')}>
              {t}
            </button>
          ))}
        </div>
      )}
      <div role="tabpanel" className="flex-1 overflow-y-auto">
        {!both || tab === 'install' ? renderInstall(c, def) : renderConnect(c, def)}
        {renderActivity && c.mode !== '' && renderActivity(c, def)}
      </div>
    </aside>
  )
}
