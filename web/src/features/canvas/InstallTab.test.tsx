import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { InstallTab } from './InstallTab'
import { RuntimesPanel } from './RuntimesPanel'
import { mockApi, renderPage } from '@/test/utils'
import type { CanvasComponent, CatalogComponent, Workspace } from '@/api/types'

const mongo: CatalogComponent = {
  id: 'mongodb', name: 'MongoDB', monogram: 'Mg', categories: ['database'], kind: 'service', description: '', supportedModes: ['install', 'connect'],
  versions: { default: '8.0.4', supported: ['8.0.4', '7.0.14', '6.0.19'] }, install: { strategy: 'image', ports: [27017], volumes: [{ name: 'data', path: '/data/db', sizeGB: 10 }] },
  resources: { cpu: 0.5, ramMB: 1024, storageGB: 10 },
}
const comp = (o: Partial<CanvasComponent> = {}): CanvasComponent => ({
  id: 'c1', workspaceId: 'w1', catalogId: 'mongodb', mode: '', version: '8.0.4', settings: {}, desiredState: 'draft', observedState: 'draft', progress: 0, x: 0, y: 0, ...o,
})
const capacity = { cpu: 4, ramMb: 8192, storageGb: 40, freeCpu: 2.1, freeRamMb: 4710, freeStorageGb: 22 }

afterEach(() => vi.unstubAllGlobals())

describe('InstallTab', () => {
  it('shows versions, requirements and live free capacity', async () => {
    mockApi({ 'GET /api/workspaces/w1/capacity': () => capacity })
    renderPage(<InstallTab workspaceId="w1" component={comp()} def={mongo} />)
    expect(screen.getByRole('combobox', { name: 'Version' })).toHaveValue('8.0.4')
    expect(screen.getByText('Also available: 7.0.14, 6.0.19')).toBeInTheDocument()
    expect(screen.getByDisplayValue('27017')).toBeInTheDocument()
    expect(await screen.findByText(/2.1 vCPU, 4.6 GB RAM and 22 GB free/)).toBeInTheDocument()
  })

  it('does nothing until the user confirms, then posts the persisted choices', async () => {
    const calls = mockApi({
      'GET /api/workspaces/w1/capacity': () => capacity,
      'POST /api/workspaces/w1/components/c1/install': () => ({ status: 202, body: { id: 'op1' } }),
    })
    renderPage(<InstallTab workspaceId="w1" component={comp()} def={mongo} />)
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Version' }), '7.0.14')
    await userEvent.click(screen.getByRole('button', { name: 'Install and start' }))
    expect(calls.some((c) => c.method === 'POST')).toBe(false)
    const dlg = screen.getByRole('dialog')
    expect(dlg).toHaveTextContent('Install and start MongoDB 7.0.14?')
    await userEvent.click(screen.getAllByRole('button', { name: 'Install and start' }).at(-1)!)
    await waitFor(() => expect(calls.some((c) => c.method === 'POST')).toBe(true))
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ version: '7.0.14', start: true, confirm: true, settings: { port: 27017, volumeGb: 10 } })
  })

  it('"Install only" posts start=false', async () => {
    const calls = mockApi({ 'GET /api/workspaces/w1/capacity': () => capacity, 'POST /api/workspaces/w1/components/c1/install': () => ({ status: 202, body: {} }) })
    renderPage(<InstallTab workspaceId="w1" component={comp()} def={mongo} />)
    await userEvent.click(screen.getByRole('button', { name: 'Install and start' }))
    await userEvent.click(screen.getByRole('button', { name: 'Install only' }))
    await waitFor(() => expect(calls.find((c) => c.method === 'POST')?.body).toMatchObject({ start: false }))
  })

  it('surfaces a port conflict next to the port field', async () => {
    mockApi({
      'GET /api/workspaces/w1/capacity': () => capacity,
      'POST /api/workspaces/w1/components/c1/install': () => ({ status: 422, body: { error: { code: 'port_conflict', message: 'Port 27017 is already used by Kafka.', field: 'port' } } }),
    })
    renderPage(<InstallTab workspaceId="w1" component={comp()} def={mongo} />)
    await userEvent.click(screen.getByRole('button', { name: 'Install and start' }))
    await userEvent.click(screen.getAllByRole('button', { name: 'Install and start' }).at(-1)!)
    expect((await screen.findAllByText('Port 27017 is already used by Kafka.')).length).toBeGreaterThan(0)
  })

  it('failed install shows the reason and offers a retry', () => {
    mockApi({ 'GET /api/workspaces/w1/capacity': () => capacity })
    renderPage(<InstallTab workspaceId="w1" component={comp({ mode: 'installed', observedState: 'failed', message: 'start failed: port 9092 already in use' })} def={mongo} />)
    expect(screen.getByRole('alert')).toHaveTextContent('start failed: port 9092 already in use')
    expect(screen.getByRole('button', { name: 'Retry install' })).toBeInTheDocument()
  })

  it('installed running service offers Stop and Remove, not Install', () => {
    mockApi({ 'GET /api/workspaces/w1/operations': () => ({ operations: [] }) })
    renderPage(<InstallTab workspaceId="w1" component={comp({ mode: 'installed', observedState: 'running', settings: { port: 27017 } })} def={mongo} />)
    expect(screen.getByRole('button', { name: 'Stop' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Remove…' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Install/ })).not.toBeInTheDocument()
    expect(screen.getByText('mongodb:27017')).toBeInTheDocument()
  })
})

describe('RuntimesPanel', () => {
  const defs = new Map<string, CatalogComponent>([
    ['python', { ...mongo, id: 'python', name: 'Python', kind: 'runtime', versions: { default: '3.12.7', supported: ['3.12.7', '3.11.10'] } }],
    ['nodejs', { ...mongo, id: 'nodejs', name: 'Node.js', kind: 'runtime', versions: { default: '22.11.0', supported: ['22.11.0'] } }],
    ['go', { ...mongo, id: 'go', name: 'Go', kind: 'runtime', versions: { default: '1.23.3', supported: ['1.23.3'] } }],
  ])
  const ws = { id: 'w1', runtimes: { python: { installed: ['3.12.7'], active: '3.12.7' }, nodejs: { installed: ['22.11.0'], active: '22.11.0' } } } as unknown as Workspace

  it('shows active versions, hides installed ones from the picker, and installs another', async () => {
    const calls = mockApi({ 'GET /api/workspaces/w1/operations': () => ({ operations: [] }), 'POST /api/workspaces/w1/runtimes/python/install': () => ({ status: 202, body: {} }) })
    renderPage(<RuntimesPanel workspace={ws} defs={defs} onClose={() => {}} />)
    expect(screen.getAllByText(/active/)[0]).toBeInTheDocument()
    expect(screen.getByText('not installed')).toBeInTheDocument() // Go
    expect(screen.getByText('All supported versions are installed.')).toBeInTheDocument() // Node.js
    const py = screen.getByRole('region', { name: 'Python runtime' })
    expect(py).toHaveTextContent('Installed: 3.12.7')
    await userEvent.click(py.querySelector('button')!)
    await waitFor(() => expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ version: '3.11.10', activate: true }))
  })
})
