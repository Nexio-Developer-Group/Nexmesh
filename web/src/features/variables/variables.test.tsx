import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { VariablesTab } from './VariablesTab'
import { mockApi, renderPage } from '@/test/utils'

const listing = {
  variables: [
    { name: 'STRIPE_KEY', secret: true, description: 'payments', updatedAt: '2026-01-01T00:00:00Z' },
    { name: 'LOG_LEVEL', secret: false, value: 'debug', updatedAt: '2026-01-01T00:00:00Z' },
    { name: 'PROD_ORDERS_DB_URI', secret: false, value: 'mongodb://x', overridesGenerated: true, updatedAt: '2026-01-01T00:00:00Z' },
  ],
  generated: [
    { name: 'PROD_ORDERS_DB_URI', componentId: 'c1', componentName: 'prod-orders-db', overridden: true },
    { name: 'PROD_ORDERS_DB_HOST', componentId: 'c1', componentName: 'prod-orders-db' },
  ],
}

function setup(extra: Record<string, (url: string, init?: RequestInit) => unknown> = {}) {
  return mockApi({
    'GET /api/workspaces/ws1/variables': () => listing,
    'GET /api/workspaces/ws1/components': () => ({ components: [{ id: 'c1', catalogId: 'mongodb', connName: 'prod-orders-db' }] }),
    ...extra,
  })
}

describe('VariablesTab', () => {
  it('masks secrets, shows plain values and read-only generated rows', async () => {
    setup()
    renderPage(<VariablesTab workspaceId="ws1" />)
    const row = (await screen.findByText('STRIPE_KEY')).closest('tr')!
    expect(within(row).getByText('Secret')).toBeInTheDocument()
    expect(row.textContent).not.toMatch(/sk_/)
    expect(screen.getByText('debug')).toBeInTheDocument()
    expect(screen.getByText('Overrides generated')).toBeInTheDocument()
    const gen = screen.getByText('PROD_ORDERS_DB_HOST').closest('tr')!
    expect(within(gen).getByText('Read-only')).toBeInTheDocument()
    expect(within(gen).queryByRole('button')).toBeNull()
  })

  it('saves a secret without echoing it and keeps the value when editing blank', async () => {
    const calls = setup({ 'PUT /api/workspaces/ws1/variables/': () => ({ name: 'X', secret: true, updatedAt: '' }) })
    renderPage(<VariablesTab workspaceId="ws1" />)
    await userEvent.click(await screen.findByRole('button', { name: 'Add variable' }))
    await userEvent.type(screen.getByLabelText('Name'), 'NEW_TOKEN')
    await userEvent.click(screen.getByLabelText(/Secret/))
    const val = screen.getByLabelText('Value')
    expect(val).toHaveAttribute('type', 'password')
    await userEvent.type(val, 'abc123')
    await userEvent.click(screen.getByRole('button', { name: 'Save variable' }))
    await waitFor(() => expect(calls.find((c) => c.method === 'PUT')).toBeTruthy())
    expect(calls.find((c) => c.method === 'PUT')).toMatchObject({ url: '/api/workspaces/ws1/variables/NEW_TOKEN', body: { secret: true, value: 'abc123', componentId: '' } })

    await userEvent.click(await screen.findByRole('button', { name: 'Edit STRIPE_KEY' }))
    expect(screen.getByLabelText('Value')).toHaveValue('')
    await userEvent.clear(screen.getByLabelText(/Description/))
    await userEvent.type(screen.getByLabelText(/Description/), 'billing')
    await userEvent.click(screen.getByRole('button', { name: 'Save variable' }))
    await waitFor(() => expect(calls.filter((c) => c.method === 'PUT')).toHaveLength(2))
    const second = calls.filter((c) => c.method === 'PUT')[1].body as Record<string, unknown>
    expect('value' in second).toBe(false)
    expect(second.description).toBe('billing')
  })

  it('shows server validation on the name field', async () => {
    setup({ 'PUT /api/workspaces/ws1/variables/': () => ({ status: 422, body: { error: { code: 'invalid', message: 'Names starting with NEXMESH_ are reserved for Nexmesh.', field: 'name' } } }) })
    renderPage(<VariablesTab workspaceId="ws1" />)
    await userEvent.click(await screen.findByRole('button', { name: 'Add variable' }))
    await userEvent.type(screen.getByLabelText('Name'), 'NEXMESH_X')
    await userEvent.type(screen.getByLabelText('Value'), 'v')
    await userEvent.click(screen.getByRole('button', { name: 'Save variable' }))
    expect(await screen.findByText(/reserved for Nexmesh/)).toBeInTheDocument()
  })

  it('previews an import before applying it', async () => {
    const calls = setup({
      'POST /api/workspaces/ws1/variables/import': (_u, init) => {
        const b = JSON.parse(String(init?.body))
        return { created: ['A'], updated: [], skipped: ['B'], secrets: ['A'], errors: [{ line: 3, message: 'Expected NAME=value.' }], dryRun: b.dryRun }
      },
    })
    renderPage(<VariablesTab workspaceId="ws1" />)
    await userEvent.click(await screen.findByRole('button', { name: 'Import .env' }))
    const importBtn = screen.getByRole('button', { name: 'Import' })
    expect(importBtn).toBeDisabled()
    await userEvent.type(screen.getByLabelText('Paste the file contents'), 'A=1')
    expect(importBtn).toBeDisabled() // must preview first
    await userEvent.click(screen.getByRole('button', { name: 'Preview' }))
    expect(await screen.findByText(/Preview: 1 new, 0 updated, 1 skipped, 1 secret/)).toBeInTheDocument()
    expect(screen.getByText(/Line 3: Expected NAME=value./)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Import' }))
    await waitFor(() => expect(screen.getByText(/Imported: 1 new/)).toBeInTheDocument())
    expect(calls.filter((c) => c.method === 'POST').map((c) => (c.body as { dryRun: boolean }).dryRun)).toEqual([true, false])
  })
})
