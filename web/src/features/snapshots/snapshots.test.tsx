import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SnapshotsTab } from './SnapshotsTab'
import { mockApi, renderPage } from '@/test/utils'
import type { Workspace } from '@/api/types'

const ws = (status: Workspace['status']): Workspace => ({
  id: 'ws1', name: 'dev', description: '', os: 'ubuntu-24.04', cpu: 2, ramGb: 4, storageGb: 20, gpu: false, status, step: status,
  createdAt: '', lastOpenedAt: '', runtimes: {}, restartRequired: false, agentConnected: true, idleStopMinutes: 0,
})
const snap = (id: string, name: string, state = 'ready', extra = {}) => ({ id, name, state, sizeBytes: 5 * (1 << 20), createdAt: '2026-03-01T12:00:00Z', createdBy: 'u1', ...extra })

describe('SnapshotsTab', () => {
  it('lists snapshots newest first and gates restore on the workspace being stopped', async () => {
    mockApi({ 'GET /api/workspaces/ws1/snapshots': () => ({ snapshots: [snap('s1', 'first'), snap('s2', 'second')], max: 5 }) })
    const { unmount } = renderPage(<SnapshotsTab workspace={ws('ready')} />)
    const items = await screen.findAllByRole('listitem')
    expect(items[0]).toHaveTextContent('second')
    expect(within(items[0]).getByText(/5.0 MB/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Restore second' })).toBeDisabled()
    expect(screen.getByText(/stop the workspace first/i)).toBeInTheDocument()
    unmount()

    mockApi({ 'GET /api/workspaces/ws1/snapshots': () => ({ snapshots: [snap('s1', 'first')], max: 5 }) })
    renderPage(<SnapshotsTab workspace={ws('stopped')} />)
    expect(await screen.findByRole('button', { name: 'Restore first' })).toBeEnabled()
  })

  it('creates a snapshot, shows progress and failures', async () => {
    const calls = mockApi({
      'GET /api/workspaces/ws1/snapshots': () => ({ snapshots: [snap('s1', 'bad', 'failed', { error: 'no space left on device' }), snap('s2', 'running', 'creating')], max: 5 }),
      'POST /api/workspaces/ws1/snapshots': () => ({ status: 202, body: snap('s3', 'new', 'creating') }),
    })
    renderPage(<SnapshotsTab workspace={ws('ready')} />)
    expect(await screen.findByText(/saving…/)).toBeInTheDocument()
    expect(screen.getByRole('alert')).toHaveTextContent('no space left on device')
    expect(screen.getByRole('button', { name: 'Delete running' })).toBeDisabled()
    await userEvent.click(screen.getByRole('button', { name: 'Create snapshot' }))
    expect(within(screen.getByRole('dialog')).getByRole('button', { name: 'Create snapshot' })).toBeDisabled()
    await userEvent.type(screen.getByLabelText('Name'), 'before refactor')
    await userEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Create snapshot' }))
    await waitFor(() => expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ name: 'before refactor' }))
  })

  it('confirms before restoring and before deleting', async () => {
    const calls = mockApi({
      'GET /api/workspaces/ws1/snapshots': () => ({ snapshots: [snap('s1', 'first')], max: 5 }),
      'POST /api/workspaces/ws1/snapshots/s1/restore': () => ({ status: 202, body: { id: 'op1' } }),
      'DELETE /api/workspaces/ws1/snapshots/s1': () => ({ status: 204 }),
    })
    renderPage(<SnapshotsTab workspace={ws('stopped')} />)
    await userEvent.click(await screen.findByRole('button', { name: 'Restore first' }))
    expect(screen.getByRole('dialog')).toHaveTextContent(/Changes made since/)
    expect(calls.some((c) => c.method === 'POST')).toBe(false)
    await userEvent.click(screen.getByRole('button', { name: 'Replace files with this snapshot' }))
    await waitFor(() => expect(calls.find((c) => c.url.endsWith('/restore'))?.body).toEqual({ confirm: true }))

    await userEvent.click(await screen.findByRole('button', { name: 'Delete first' }))
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false)
    await userEvent.click(screen.getByRole('button', { name: 'Delete snapshot' }))
    await waitFor(() => expect(calls.some((c) => c.method === 'DELETE')).toBe(true))
  })

  it('disables creating when the limit is reached', async () => {
    mockApi({ 'GET /api/workspaces/ws1/snapshots': () => ({ snapshots: [1, 2, 3, 4, 5].map((n) => snap('s' + n, 'n' + n)), max: 5 }) })
    renderPage(<SnapshotsTab workspace={ws('ready')} />)
    expect(await screen.findByText(/the most it can keep/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Create snapshot' })).toBeDisabled()
  })
})
