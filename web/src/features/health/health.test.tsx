import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ComponentActivity } from './ComponentActivity'
import { mockApi, renderPage } from '@/test/utils'
import type { CanvasComponent } from '@/api/types'

const comp = { id: 'c1', workspaceId: 'ws1', catalogId: 'redis', mode: 'installed', observedState: 'running' } as unknown as CanvasComponent

const sample = (at: string, state: string, healthy: boolean, message = '') => ({ at, state, healthy, message })
const history = {
  uptimePercent: 66.6,
  since: new Date(Date.now() - 12 * 60000).toISOString(),
  samples: [ // newest first
    sample('2026-03-01T12:30:00Z', 'running', true),
    sample('2026-03-01T12:20:00Z', 'failed', false, 'Container exited with code 1'),
    sample('2026-03-01T12:10:00Z', 'running', true),
  ],
}

describe('ComponentActivity', () => {
  it('shows the current state, uptime and the recent state changes', async () => {
    mockApi({ 'GET /api/workspaces/ws1/components/c1/health': () => history })
    renderPage(<ComponentActivity workspaceId="ws1" component={comp} hasLogs={false} />)
    const health = await screen.findByRole('region', { name: 'Health history' })
    expect(await within(health).findByText(/67% healthy/)).toBeInTheDocument()
    expect(within(health).getByText(/for 12 min/)).toBeInTheDocument()
    const changes = within(health).getByRole('list', { name: 'Recent state changes' })
    expect(within(changes).getByText('Failed')).toBeInTheDocument()
    expect(within(changes).getByText('Container exited with code 1')).toBeInTheDocument()
    expect(within(health).getByRole('img', { name: /last 3 checks/ })).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'Logs' })).toBeNull() // no logs for connected-only
  })

  it('loads logs on demand and offers refresh', async () => {
    let n = 0
    const calls = mockApi({
      'GET /api/workspaces/ws1/components/c1/health': () => history,
      'GET /api/workspaces/ws1/components/c1/logs': () => ({ logs: n++ === 0 ? 'ready to accept connections\n' : 'second line\n' }),
    })
    renderPage(<ComponentActivity workspaceId="ws1" component={comp} hasLogs />)
    await screen.findByRole('region', { name: 'Health history' })
    expect(calls.some((c) => c.url.includes('/logs'))).toBe(false) // nothing fetched until opened
    await userEvent.click(screen.getByRole('button', { name: 'Show logs' }))
    expect(await screen.findByText('ready to accept connections')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
    await waitFor(() => expect(screen.getByLabelText('Service logs')).toHaveTextContent('second line'))
    await userEvent.click(screen.getByRole('button', { name: 'Hide logs' }))
    expect(screen.queryByLabelText('Service logs')).toBeNull()
  })

  it('says so when nothing was recorded or the service printed nothing', async () => {
    mockApi({
      'GET /api/workspaces/ws1/components/c1/health': () => ({ samples: [], uptimePercent: 0 }),
      'GET /api/workspaces/ws1/components/c1/logs': () => ({ logs: '' }),
    })
    renderPage(<ComponentActivity workspaceId="ws1" component={comp} hasLogs />)
    expect(await screen.findByText('No checks recorded yet.')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Show logs' }))
    expect(await screen.findByText('No output yet.')).toBeInTheDocument()
  })

  it('surfaces API errors', async () => {
    mockApi({ 'GET /api/workspaces/ws1/components/c1/health': () => ({ status: 404, body: { error: { code: 'not_found', message: 'Not found.' } } }) })
    renderPage(<ComponentActivity workspaceId="ws1" component={comp} hasLogs={false} />)
    expect(await screen.findByRole('alert')).toHaveTextContent('Not found.')
  })
})
