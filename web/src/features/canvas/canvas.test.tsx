import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ReactFlowProvider } from '@xyflow/react'
import { CatalogSidebar } from './CatalogSidebar'
import { PropertiesPanel } from './PropertiesPanel'
import { TechNode } from './TechNode'
import type { CanvasComponent, Catalog, CatalogComponent } from '@/api/types'

const def = (id: string, name: string, cats: string[], modes: ('install' | 'connect')[], kind: CatalogComponent['kind'] = 'service'): CatalogComponent => ({
  id, name, monogram: name.slice(0, 2), categories: cats, kind, description: '', supportedModes: modes, versions: { default: '1.0', supported: ['1.0'] },
})
const catalog: Catalog = {
  categories: [
    { id: 'database', label: 'Database', items: ['mongodb'] },
    { id: 'backend', label: 'Backend', items: ['nestjs'] },
    { id: 'scripting', label: 'Scripting', items: ['bash'] },
  ],
  components: [def('mongodb', 'MongoDB', ['database'], ['install', 'connect']), def('nestjs', 'NestJS', ['backend'], ['install'], 'framework'), def('bash', 'Bash', ['scripting'], ['install'], 'scripting')],
}
const comp = (over: Partial<CanvasComponent> = {}): CanvasComponent => ({
  id: 'c1', workspaceId: 'w', catalogId: 'mongodb', mode: '', version: '8.0.4', settings: {}, desiredState: 'draft', observedState: 'draft', progress: 0, x: 0, y: 0, ...over,
})

describe('CatalogSidebar', () => {
  const props = { catalog, onCanvas: [comp()], runtimes: [{ id: 'python', name: 'Python', active: '3.12.7', installed: ['3.12.7'] }, { id: 'go', name: 'Go', installed: [] }] }

  it('lists categories, marks items on the canvas, and collapses the rest', () => {
    render(<CatalogSidebar {...props} onAdd={() => {}} />)
    expect(screen.getByText('Database')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Add MongoDB to canvas' })).toBeInTheDocument()
    expect(screen.getByLabelText('on canvas')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Add Bash to canvas' })).not.toBeInTheDocument() // scripting collapsed
    expect(screen.getByText('3.12.7 active')).toBeInTheDocument()
    expect(screen.getByText('not installed')).toBeInTheDocument()
  })

  it('search expands matching categories', async () => {
    render(<CatalogSidebar {...props} onAdd={() => {}} />)
    await userEvent.type(screen.getByLabelText('Search technologies'), 'bas')
    expect(screen.getByRole('button', { name: 'Add Bash to canvas' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Add MongoDB to canvas' })).not.toBeInTheDocument()
  })

  it('keyboard users add a technology without dragging', async () => {
    const onAdd = vi.fn()
    render(<CatalogSidebar {...props} onAdd={onAdd} />)
    await userEvent.click(screen.getByRole('button', { name: 'Add NestJS to canvas' }))
    expect(onAdd).toHaveBeenCalledWith('nestjs')
  })

  it('drag start carries the catalog id', () => {
    render(<CatalogSidebar {...props} onAdd={() => {}} />)
    const setData = vi.fn()
    const btn = screen.getByRole('button', { name: 'Add MongoDB to canvas' })
    const ev = new Event('dragstart', { bubbles: true }) as unknown as DragEvent
    Object.defineProperty(ev, 'dataTransfer', { value: { setData, effectAllowed: '' } })
    btn.dispatchEvent(ev)
    expect(setData).toHaveBeenCalledWith('application/x-nexmesh-component', 'mongodb')
  })
})

describe('PropertiesPanel', () => {
  const render_ = (def: CatalogComponent) =>
    render(<PropertiesPanel component={comp({ catalogId: def.id })} def={def} onClose={() => {}} renderInstall={() => <p>install body</p>} renderConnect={() => <p>connect body</p>} />)

  it('service components show Install and Connect tabs', async () => {
    render_(catalog.components[0])
    expect(screen.getByRole('tab', { name: 'install' })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('tab', { name: 'connect' }))
    expect(screen.getByText('connect body')).toBeInTheDocument()
  })

  it('install-only components show no tabs', () => {
    render_(catalog.components[1])
    expect(screen.queryByRole('tab')).not.toBeInTheDocument()
    expect(screen.getByText('install body')).toBeInTheDocument()
  })
})

describe('TechNode', () => {
  const node = (c: CanvasComponent, d: CatalogComponent, extra: Record<string, unknown> = {}) =>
    render(
      <ReactFlowProvider>
        <TechNode {...(({ id: c.id, data: { component: c, def: d, selected: false, ...extra } }) as unknown as Parameters<typeof TechNode>[0])} />
      </ReactFlowProvider>,
    )

  it('shows state and mode badges as text', () => {
    node(comp({ observedState: 'running', mode: 'installed' }), catalog.components[0])
    expect(screen.getByText('Running')).toBeInTheDocument()
    expect(screen.getByText('Installed')).toBeInTheDocument()
  })

  it('failed install card offers Retry and View logs', async () => {
    const onRetry = vi.fn()
    node(comp({ observedState: 'failed', mode: 'installed', message: 'start failed: port 9092 already in use' }), catalog.components[0], { onRetry })
    expect(screen.getByText(/port 9092 already in use/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(onRetry).toHaveBeenCalled()
  })

  it('installing card exposes progress to assistive tech', () => {
    node(comp({ observedState: 'installing', mode: 'installed', progress: 62 }), catalog.components[0])
    expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '62')
    expect(screen.getByText('Installing 62%')).toBeInTheDocument()
  })

  it('connected card shows its environment label', () => {
    node(comp({ observedState: 'connected', mode: 'connected', environment: 'production', connName: 'prod-orders-db' }), catalog.components[0])
    expect(screen.getByText('production')).toBeInTheDocument()
    expect(screen.getByText('prod-orders-db')).toBeInTheDocument()
  })
})
