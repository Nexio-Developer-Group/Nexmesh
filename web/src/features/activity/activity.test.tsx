import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ActivityTab } from './ActivityTab'
import { mockApi, renderPage } from '@/test/utils'

const ev = (id: string, action: string, over = {}) => ({ id, actorType: 'user', actorId: 'u1', action, target: 'x', at: '2026-03-01T12:00:00Z', ...over })

describe('ActivityTab', () => {
  it('lists events newest first with details on demand', async () => {
    mockApi({ 'GET /api/workspaces/ws1/audit?': () => ({ events: [ev('a', 'git.push', { detail: { host: 'github.com' } }), ev('b', 'component.install', { actorType: 'system', actorId: '' })] }) })
    renderPage(<ActivityTab workspaceId="ws1" />)
    expect(await screen.findByText('git.push')).toBeInTheDocument()
    const row = screen.getByText('git.push').closest('tr')!
    expect(within(row).queryByText(/github.com/)).toBeNull()
    await userEvent.click(within(row).getByRole('button', { name: /Show/ }))
    expect(within(row).getByText(/"host": "github.com"/)).toBeInTheDocument()
    expect(within(screen.getByText('component.install').closest('tr')!).getByText('System')).toBeInTheDocument()
  })

  it('applies filters to the query and to both export links', async () => {
    const calls = mockApi({ 'GET /api/workspaces/ws1/audit?': () => ({ events: [] }) })
    renderPage(<ActivityTab workspaceId="ws1" />)
    expect(await screen.findByText('No activity matches these filters.')).toBeInTheDocument()
    await userEvent.selectOptions(screen.getByLabelText('Category'), 'git.')
    await userEvent.selectOptions(screen.getByLabelText('Who'), 'agent')
    await userEvent.type(screen.getByLabelText('Search'), 'push')
    await userEvent.click(screen.getByRole('button', { name: 'Apply' }))
    await waitFor(() => expect(calls.some((c) => c.url.includes('action=git.') && c.url.includes('actor=agent') && c.url.includes('q=push'))).toBe(true))
    const csv = screen.getByRole('link', { name: 'Export CSV' }).getAttribute('href')!
    expect(csv).toContain('/audit/export?')
    expect(csv).toContain('action=git.')
    expect(csv).toContain('format=csv')
    expect(screen.getByRole('link', { name: 'Export JSON' }).getAttribute('href')).toContain('format=json')
    await userEvent.click(screen.getByRole('button', { name: 'Clear' }))
    expect(screen.getByLabelText('Search')).toHaveValue('')
  })

  it('loads more pages with the cursor', async () => {
    const calls = mockApi({
      'GET /api/workspaces/ws1/audit?': (u) => (u.includes('after=a') ? { events: [ev('b', 'file.write')] } : { events: [ev('a', 'git.push')], next: 'a' }),
    })
    renderPage(<ActivityTab workspaceId="ws1" />)
    await userEvent.click(await screen.findByRole('button', { name: 'Load more' }))
    expect(await screen.findByText('file.write')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Load more' })).toBeNull()
    expect(calls.filter((c) => c.method === 'GET')).toHaveLength(2)
  })
})
