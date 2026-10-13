import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ConnectTab, maskConnectionString } from './ConnectTab'
import { mockApi, renderPage } from '@/test/utils'
import type { CanvasComponent, CatalogComponent, TestResult } from '@/api/types'

const mongo: CatalogComponent = {
  id: 'mongodb', name: 'MongoDB', monogram: 'Mg', categories: ['database'], kind: 'service', description: '', supportedModes: ['install', 'connect'],
  versions: { default: '8.0.4', supported: ['8.0.4'] },
  connection: {
    schemes: ['mongodb', 'mongodb+srv'], defaultPort: 27017, envPrefix: 'MONGODB', fields: [
      { name: 'host', label: 'Host', type: 'string', required: true, advanced: false },
      { name: 'port', label: 'Port', type: 'number', required: false, advanced: false, default: 27017 },
      { name: 'database', label: 'Database', type: 'string', required: true, advanced: false },
      { name: 'username', label: 'Username', type: 'string', required: true, advanced: false },
      { name: 'password', label: 'Password', type: 'password', required: true, advanced: false },
      { name: 'authSource', label: 'Auth source', type: 'string', required: false, advanced: true, default: 'admin' },
      { name: 'tls', label: 'TLS', type: 'boolean', required: false, advanced: true, default: true },
      { name: 'tlsInsecure', label: 'Skip certificate verification', type: 'boolean', required: false, advanced: true, default: false },
    ],
  },
}
const card = (o: Partial<CanvasComponent> = {}): CanvasComponent => ({
  id: 'c1', workspaceId: 'w1', catalogId: 'mongodb', mode: '', version: '8.0.4', settings: {}, desiredState: 'draft', observedState: 'draft', progress: 0, x: 0, y: 0, ...o,
})
const pass: TestResult = {
  passed: true, at: '2026-10-13T12:00:00Z', latencyMs: 41, serverVersion: '7.0.14',
  stages: ['input_validation', 'dns', 'network', 'tls', 'authentication', 'authorization', 'server_details'].map((stage) => ({ stage, status: 'passed' as const, message: 'ok' })),
}
const fail: TestResult = {
  passed: false, failedStage: 'authentication', message: 'Host reachable but authentication failed.', at: '2026-10-13T12:00:00Z',
  stages: [
    ...['input_validation', 'dns', 'network', 'tls'].map((stage) => ({ stage, status: 'passed' as const })),
    { stage: 'authentication', status: 'failed' as const, message: 'Host reachable but authentication failed. Check the username and password.' },
    { stage: 'authorization', status: 'skipped' as const }, { stage: 'server_details', status: 'skipped' as const },
  ],
}

async function fillBasics() {
  await userEvent.type(screen.getByLabelText('Connection name'), 'prod-orders-db')
  await userEvent.type(screen.getByLabelText(/^Host/), 'orders.k3x9p.mongodb.net')
  await userEvent.type(screen.getByLabelText(/^Database/), 'orders')
  await userEvent.type(screen.getByLabelText(/^Username/), 'orders_app')
  await userEvent.type(screen.getByLabelText(/^Password/), 'S3cretPassw0rd')
}

afterEach(() => vi.unstubAllGlobals())

describe('ConnectTab form', () => {
  it('Add to Workspace stays disabled until a test passes for the current values', async () => {
    const calls = mockApi({ 'POST /api/workspaces/w1/components/c1/connection/test': () => pass })
    renderPage(<ConnectTab workspaceId="w1" component={card()} def={mongo} />)
    const add = screen.getByRole('button', { name: 'Add to Workspace' })
    expect(add).toBeDisabled()
    await fillBasics()
    expect(add).toBeDisabled()
    await userEvent.click(screen.getByRole('button', { name: 'Test Connection' }))
    await waitFor(() => expect(add).toBeEnabled())
    expect(screen.getByText('Test passed for these exact values.')).toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent('Passed · 7 of 7 checks')
    // The body sent to the server carries the password; it is typed into a password input.
    expect(calls[0].body).toMatchObject({ connName: 'prod-orders-db', fields: { host: 'orders.k3x9p.mongodb.net', password: 'S3cretPassw0rd' } })
    expect(screen.getByLabelText(/^Password/)).toHaveAttribute('type', 'password')
  })

  it('editing any field after a pass disables Add again', async () => {
    mockApi({ 'POST /api/workspaces/w1/components/c1/connection/test': () => pass })
    renderPage(<ConnectTab workspaceId="w1" component={card()} def={mongo} />)
    await fillBasics()
    await userEvent.click(screen.getByRole('button', { name: 'Test Connection' }))
    const add = screen.getByRole('button', { name: 'Add to Workspace' })
    await waitFor(() => expect(add).toBeEnabled())
    await userEvent.type(screen.getByLabelText(/^Database/), 'x')
    expect(add).toBeDisabled()
    expect(screen.getByText(/Values changed since the last test/)).toBeInTheDocument()
  })

  it('a failed test keeps the entered values and shows the specific reason', async () => {
    mockApi({ 'POST /api/workspaces/w1/components/c1/connection/test': () => fail })
    renderPage(<ConnectTab workspaceId="w1" component={card()} def={mongo} />)
    await fillBasics()
    await userEvent.click(screen.getByRole('button', { name: 'Test Connection' }))
    expect(await screen.findByText(/Host reachable but authentication failed. Check the username and password./)).toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent('Failed at Authentication')
    expect(screen.getByRole('button', { name: 'Add to Workspace' })).toBeDisabled()
    expect(screen.getByLabelText(/^Host/)).toHaveValue('orders.k3x9p.mongodb.net')
    expect(screen.getByLabelText(/^Password/)).toHaveValue('S3cretPassw0rd')
    expect(screen.getByRole('button', { name: 'Retest' })).toBeInTheDocument()
  })

  it('shows a production warning banner when the environment is production', async () => {
    renderPage(<ConnectTab workspaceId="w1" component={card()} def={mongo} />)
    expect(screen.queryByText(/Production label/)).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('radio', { name: 'production' }))
    expect(screen.getByText(/Production label/)).toBeInTheDocument()
  })

  it('pasting a connection string fills the fields and masks the password', async () => {
    mockApi({
      'POST /api/connections/parse': () => ({ fields: { host: 'orders.k3x9p.mongodb.net', port: 27017, database: 'orders', username: 'orders_app', password: 'p@ss', authSource: 'admin' } }),
    })
    renderPage(<ConnectTab workspaceId="w1" component={card()} def={mongo} />)
    await userEvent.click(screen.getByLabelText(/Paste connection string/))
    await userEvent.paste('mongodb+srv://orders_app:p%40ss@orders.k3x9p.mongodb.net/orders')
    await waitFor(() => expect(screen.getByLabelText(/^Host/)).toHaveValue('orders.k3x9p.mongodb.net'))
    expect(screen.getByLabelText(/^Username/)).toHaveValue('orders_app')
    expect(screen.getByLabelText(/^Password/)).toHaveValue('p@ss')
    const pasted = screen.getByLabelText(/Paste connection string/) as HTMLInputElement
    expect(pasted.value).toContain('••••')
    expect(pasted.value).not.toContain('p%40ss')
  })

  it('reports an unparsable connection string next to the field', async () => {
    mockApi({ 'POST /api/connections/parse': () => ({ status: 422, body: { error: { code: 'invalid_connection_string', message: 'Expected a connection string starting with mongodb://' } } }) })
    renderPage(<ConnectTab workspaceId="w1" component={card()} def={mongo} />)
    await userEvent.click(screen.getByLabelText(/Paste connection string/))
    await userEvent.paste('mysql://x')
    expect(await screen.findByText(/Expected a connection string/)).toBeInTheDocument()
  })

  it('after a reload a passed test still enables Add without retyping the write-only password', () => {
    renderPage(<ConnectTab workspaceId="w1" component={card({ observedState: 'test_passed', lastTest: pass, connName: 'prod-orders-db', connection: { host: 'h', database: 'd', username: 'u', port: 27017 } })} def={mongo} />)
    expect(screen.getByRole('button', { name: 'Add to Workspace' })).toBeEnabled()
    expect(screen.getByLabelText(/^Password/)).toHaveAttribute('placeholder', expect.stringContaining('stored'))
  })

  it('advanced section carries a TLS badge and the insecure option warns', async () => {
    renderPage(<ConnectTab workspaceId="w1" component={card()} def={mongo} />)
    expect(screen.getByText('TLS on')).toBeInTheDocument()
    await userEvent.click(screen.getByText('Advanced'))
    await userEvent.click(screen.getByRole('switch', { name: /Skip certificate verification/ }))
    expect(screen.getByRole('alert')).toHaveTextContent(/server's identity is not checked/)
  })

  it('maskConnectionString hides only the password', () => {
    expect(maskConnectionString('mongodb+srv://orders_app:p%40ss@h.example/db?x=1')).toBe('mongodb+srv://orders_app:••••@h.example/db?x=1')
    expect(maskConnectionString('redis://h:6379')).toBe('redis://h:6379')
  })
})

describe('ConnectTab connected view', () => {
  const connected = card({
    mode: 'connected', observedState: 'connected', connName: 'prod-orders-db', environment: 'production', message: 'Last check passed, 41 ms', lastTest: pass,
    connection: { host: 'orders.k3x9p.mongodb.net', database: 'orders', username: 'orders_app', port: 27017 },
  })
  const routes = { 'GET /api/workspaces/w1/connections': () => ({ connections: [{ ...connected, exposedAs: ['PROD_ORDERS_DB_URI', 'PROD_ORDERS_DB_HOST'] }] }) }

  it('shows details, exposes only variable names, and never a password value', async () => {
    mockApi(routes)
    renderPage(<ConnectTab workspaceId="w1" component={connected} def={mongo} />)
    expect(screen.getByText('Stored · write-only')).toBeInTheDocument()
    expect(screen.getByText('orders.k3x9p.mongodb.net')).toBeInTheDocument()
    expect(await screen.findByText('PROD_ORDERS_DB_URI')).toBeInTheDocument()
    expect(screen.getByText(/Last check passed, 41 ms/)).toBeInTheDocument()
    for (const name of ['Re-test', 'Edit', 'Rotate credentials', 'Disconnect']) expect(screen.getByRole('button', { name })).toBeInTheDocument()
    expect(screen.getByText(/never deletes data on the external service/)).toBeInTheDocument()
  })

  it('Disconnect asks for confirmation first', async () => {
    const calls = mockApi({ ...routes, 'DELETE /api/workspaces/w1/components/c1/connection': () => ({ status: 204 }) })
    renderPage(<ConnectTab workspaceId="w1" component={connected} def={mongo} />)
    await userEvent.click(screen.getByRole('button', { name: 'Disconnect' }))
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false)
    const dlg = screen.getByRole('dialog')
    expect(dlg).toHaveTextContent('Data on the external service is not touched')
    await userEvent.click(within(dlg).getByRole('button', { name: 'Disconnect' }))
    await waitFor(() => expect(calls.some((c) => c.method === 'DELETE')).toBe(true))
  })

  it('Edit opens the form with Save changes locked until a test passes', async () => {
    mockApi(routes)
    renderPage(<ConnectTab workspaceId="w1" component={connected} def={mongo} />)
    await userEvent.click(screen.getByRole('button', { name: 'Edit' }))
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled()
    expect(screen.getByLabelText('Connection name')).toBeDisabled()
    expect(screen.getByLabelText(/^Host/)).toHaveValue('orders.k3x9p.mongodb.net')
  })

  it('degraded connections are announced with text, not colour alone', () => {
    mockApi(routes)
    renderPage(<ConnectTab workspaceId="w1" component={{ ...connected, observedState: 'degraded', message: 'Health check failed: timed out' }} def={mongo} />)
    expect(screen.getAllByRole('status')[0]).toHaveTextContent('Health check failed: timed out')
  })
})
