import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { canConnect } from './rules'
import { ConnectionsSection } from './ConnectionsSection'
import { NewWireDialog, WireDetailsDialog } from './WireDialog'
import { mockApi, renderPage } from '@/test/utils'
import type { CanvasComponent, CatalogComponent, Wire } from '@/api/types'

const def = (id: string, name: string, categories: string[], wiring?: CatalogComponent['wiring']) =>
  ({ id, name, monogram: id.slice(0, 2), categories, kind: 'service', wiring }) as unknown as CatalogComponent
const nest = def('nestjs', 'NestJS', ['backend'], { consumes: ['database', 'messaging'], provides: true })
const mongo = def('mongodb', 'MongoDB', ['database'], { provides: true })
const react = def('react', 'React', ['frontend'], { consumes: ['backend'] })
const defs = new Map([nest, mongo, react].map((d) => [d.id, d]))
const card = (id: string, catalogId: string): CanvasComponent => ({ id, catalogId, mode: 'installed', observedState: 'running' }) as unknown as CanvasComponent
const cNest = card('c_nest', 'nestjs'), cMongo = card('c_mongo', 'mongodb'), cReact = card('c_react', 'react')

describe('canConnect (catalog rules)', () => {
  it('follows the catalog, with a reason when refused', () => {
    expect(canConnect(nest, mongo).ok).toBe(true)
    expect(canConnect(react, nest).ok).toBe(true)
    expect(canConnect(react, mongo)).toMatchObject({ ok: false, reason: expect.stringContaining('backend') })
    expect(canConnect(mongo, nest).ok).toBe(false)
    expect(canConnect(nest, react).reason).toMatch(/Other cards cannot connect to React/)
    expect(canConnect(undefined, nest).ok).toBe(false)
  })
})

describe('ConnectionsSection', () => {
  const wire: Wire = { id: 'w1', workspaceId: 'ws', fromId: 'c_nest', toId: 'c_mongo', name: 'mongodb', variables: ['MONGODB_URI'], state: 'healthy', createdAt: '' }

  it('offers only compatible, unconnected cards and opens the review', async () => {
    const onNew = vi.fn()
    renderPage(<ConnectionsSection component={cNest} def={nest} components={[cNest, cMongo, cReact]} defs={defs} wires={[]} onNew={onNew} onOpen={vi.fn()} />)
    const select = screen.getByLabelText('Connect to')
    expect(screen.getByRole('option', { name: 'MongoDB' })).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: 'React' })).toBeNull()
    await userEvent.selectOptions(select, 'c_mongo')
    await userEvent.click(screen.getByRole('button', { name: 'Review…' }))
    expect(onNew).toHaveBeenCalledWith(cNest, cMongo)
  })

  it('lists existing lines with a text state, and the reverse side shows who uses a provider', () => {
    const { unmount } = renderPage(<ConnectionsSection component={cNest} def={nest} components={[cNest, cMongo]} defs={defs} wires={[wire]} onNew={vi.fn()} onOpen={vi.fn()} />)
    expect(screen.getByText('Healthy')).toBeInTheDocument()
    expect(screen.queryByRole('option', { name: 'MongoDB' })).toBeNull() // already connected
    unmount()
    renderPage(<ConnectionsSection component={cMongo} def={mongo} components={[cNest, cMongo]} defs={defs} wires={[wire]} onNew={vi.fn()} onOpen={vi.fn()} />)
    expect(screen.getByText('Used by')).toBeInTheDocument()
    expect(screen.getByText('NestJS')).toBeInTheDocument()
    expect(screen.queryByLabelText('Connect to')).toBeNull()
  })
})

describe('NewWireDialog', () => {
  const preview = { fromId: 'c_nest', fromName: 'NestJS', toId: 'c_mongo', toName: 'MongoDB', name: 'mongodb', state: 'healthy', warnings: [],
    variables: [{ name: 'MONGODB_URI', secret: true }, { name: 'MONGODB_HOST', secret: false }] }

  it('shows names only, creates nothing until confirmed, then creates', async () => {
    const onClose = vi.fn()
    const calls = mockApi({
      'POST /api/workspaces/ws/wires/preview': () => preview,
      'POST /api/workspaces/ws/wires': () => ({ status: 201, body: { id: 'w9' } }),
    })
    renderPage(<NewWireDialog workspaceId="ws" from={cNest} to={cMongo} onClose={onClose} />)
    expect(await screen.findByText('MONGODB_URI')).toBeInTheDocument()
    expect(screen.getByText(/value hidden/)).toBeInTheDocument()
    expect(calls.some((c) => c.method === 'POST' && c.url.endsWith('/wires'))).toBe(false)
    await userEvent.click(screen.getByRole('button', { name: 'Create connection' }))
    await waitFor(() => expect(onClose).toHaveBeenCalled())
    const create = calls.find((c) => c.method === 'POST' && c.url.endsWith('/wires'))!
    expect(create.body).toMatchObject({ fromId: 'c_nest', toId: 'c_mongo', confirm: true })
  })

  it('explains a refused pair and keeps Create disabled', async () => {
    mockApi({ 'POST /api/workspaces/ws/wires/preview': () => ({ status: 422, body: { error: { code: 'invalid_pair', message: 'React connects to backend cards, and MongoDB is database.' } } }) })
    renderPage(<NewWireDialog workspaceId="ws" from={cReact} to={cMongo} onClose={vi.fn()} />)
    expect(await screen.findByRole('alert')).toHaveTextContent('React connects to backend cards')
    expect(screen.getByRole('button', { name: 'Create connection' })).toBeDisabled()
  })
})

describe('WireDetailsDialog', () => {
  it('lists the variables and removes the line', async () => {
    const onClose = vi.fn()
    const calls = mockApi({ 'DELETE /api/workspaces/ws/wires/w1': () => ({ status: 204 }) })
    const wire: Wire = { id: 'w1', workspaceId: 'ws', fromId: 'a', toId: 'b', name: 'mongodb', variables: ['MONGODB_URI'], state: 'unreachable', message: 'The provider is stopped.', createdAt: '' }
    renderPage(<WireDetailsDialog workspaceId="ws" wire={wire} fromName="NestJS" toName="MongoDB" onClose={onClose} />)
    expect(screen.getByText('Unreachable')).toBeInTheDocument()
    expect(screen.getByText('The provider is stopped.')).toBeInTheDocument()
    expect(screen.getByText('MONGODB_URI')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Remove connection' }))
    await waitFor(() => expect(onClose).toHaveBeenCalled())
    expect(calls.some((c) => c.method === 'DELETE')).toBe(true)
  })
})
