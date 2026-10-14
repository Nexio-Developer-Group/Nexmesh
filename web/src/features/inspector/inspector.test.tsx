import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { InspectorDialog } from './InspectorDialog'
import { mockApi, renderPage } from '@/test/utils'
import type { CanvasComponent, CatalogComponent } from '@/api/types'

const redis = { id: 'redis', name: 'Redis', monogram: 'Rd' } as unknown as CatalogComponent
const mysql = { id: 'mysql', name: 'MySQL', monogram: 'My' } as unknown as CatalogComponent
const card = (id: string, catalogId: string) => ({ id, catalogId, connName: 'cache', mode: 'connected', observedState: 'connected' }) as unknown as CanvasComponent

const redisCaps = {
  available: true, readOnly: true,
  ops: [
    { name: 'keys', label: 'Browse keys', args: [{ name: 'pattern', label: 'Pattern' }, { name: 'limit', label: 'Keys per page', kind: 'number' }] },
    { name: 'get', label: 'Read a key', args: [{ name: 'key', label: 'Key', required: true }] },
    { name: 'command', label: 'Read command', args: [{ name: 'command', label: 'Command', required: true, kind: 'code' }] },
  ],
}

describe('InspectorDialog', () => {
  it('explains when a card cannot be inspected', async () => {
    mockApi({ 'GET /api/workspaces/ws/components/c1/inspect': () => ({ available: false, reason: 'Connect this card to a service first.', readOnly: true, ops: [] }) })
    renderPage(<InspectorDialog workspaceId="ws" component={card('c1', 'redis')} def={redis} onClose={vi.fn()} />)
    expect(await screen.findByRole('status')).toHaveTextContent('Connect this card to a service first.')
    expect(screen.queryByRole('button', { name: 'Run' })).toBeNull()
  })

  it('states that it is read-only, builds the form from the operation, and shows results', async () => {
    const calls = mockApi({
      'GET /api/workspaces/ws/components/c1/inspect': () => redisCaps,
      'POST /api/workspaces/ws/components/c1/inspect': () => ({ columns: ['key', 'type', 'ttl'], rows: [['user:1', 'string', 'no expiry'], ['user:2', 'hash', null]], elapsedMs: 4, note: 'More keys exist.', next: '17' }),
    })
    renderPage(<InspectorDialog workspaceId="ws" component={card('c1', 'redis')} def={redis} onClose={vi.fn()} />)
    expect(await screen.findByText('✓ Read-only')).toBeInTheDocument()
    expect(screen.getByText(/each request is recorded in the audit log without them/)).toBeInTheDocument()
    await userEvent.type(screen.getByLabelText(/^Pattern/), 'user:*')
    await userEvent.click(screen.getByRole('button', { name: 'Run' }))
    const region = await screen.findByRole('region', { name: 'Results' })
    expect(within(region).getByText('user:1')).toBeInTheDocument()
    expect(within(region).getByText('null')).toBeInTheDocument()
    expect(within(region).getByText('More keys exist.')).toBeInTheDocument()
    expect(calls.find((c) => c.method === 'POST')!.body).toEqual({ op: 'keys', args: { pattern: 'user:*' } })
    // Paging continues from the cursor with the same arguments.
    await userEvent.click(within(region).getByRole('button', { name: 'Next page' }))
    await waitFor(() => expect(calls.filter((c) => c.method === 'POST')[1].body).toEqual({ op: 'keys', args: { pattern: 'user:*', cursor: '17' } }))
  })

  it('drills from a key into its value', async () => {
    const calls = mockApi({
      'GET /api/workspaces/ws/components/c1/inspect': () => redisCaps,
      'POST /api/workspaces/ws/components/c1/inspect': (_u, i) => {
        const b = JSON.parse(String(i?.body))
        return b.op === 'get' ? { columns: ['value'], rows: [['hello']], elapsedMs: 1 } : { columns: ['key', 'type', 'ttl'], rows: [['user:1', 'string', 'no expiry']], elapsedMs: 1 }
      },
    })
    renderPage(<InspectorDialog workspaceId="ws" component={card('c1', 'redis')} def={redis} onClose={vi.fn()} />)
    await userEvent.click(await screen.findByRole('button', { name: 'Run' }))
    await userEvent.click(await screen.findByRole('button', { name: 'user:1' }))
    expect(await screen.findByText('hello')).toBeInTheDocument()
    expect(calls.filter((c) => c.method === 'POST').pop()!.body).toMatchObject({ op: 'get', args: { key: 'user:1' } })
    expect(screen.getByLabelText(/^Key/)).toHaveValue('user:1')
  })

  it('requires the required arguments, and shows a refused statement in words', async () => {
    mockApi({
      'GET /api/workspaces/ws/components/c1/inspect': () => redisCaps,
      'POST /api/workspaces/ws/components/c1/inspect': () => ({ status: 422, body: { error: { code: 'read_only', message: 'SET is not a read command, so it is not allowed here.' } } }),
    })
    renderPage(<InspectorDialog workspaceId="ws" component={card('c1', 'redis')} def={redis} onClose={vi.fn()} />)
    await userEvent.selectOptions(await screen.findByLabelText('Operation'), 'command')
    const run = screen.getByRole('button', { name: 'Run' })
    expect(run).toBeDisabled()
    await userEvent.type(screen.getByLabelText(/^Command/), 'SET a b')
    expect(run).toBeEnabled()
    await userEvent.click(run)
    expect(await screen.findByRole('alert')).toHaveTextContent('not allowed here')
  })

  it('marks production connections and shows SQL as a code box', async () => {
    mockApi({
      'GET /api/workspaces/ws/components/c2/inspect': () => ({ available: true, readOnly: true, production: true, ops: [{ name: 'query', label: 'Read-only query', args: [{ name: 'sql', label: 'SQL', required: true, kind: 'code' }] }] }),
    })
    renderPage(<InspectorDialog workspaceId="ws" component={card('c2', 'mysql')} def={mysql} onClose={vi.fn()} />)
    expect(await screen.findByRole('note')).toHaveTextContent('Production')
    expect(screen.getByLabelText(/^SQL/).tagName).toBe('TEXTAREA')
  })
})
