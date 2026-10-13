import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { GeneralTab } from './GeneralTab'
import { ResourcesTab } from './ResourcesTab'
import { mockApi, renderPage } from '@/test/utils'
import type { Workspace } from '@/api/types'

const ws: Workspace = {
  id: 'w1', name: 'orders-api', description: 'Stripe webhooks', os: 'ubuntu-24.04', cpu: 4, ramGb: 8, storageGb: 40, gpu: false, status: 'ready', step: 'ready',
  createdAt: '2026-10-13T10:00:00Z', lastOpenedAt: '2026-10-13T10:00:00Z', runtimes: {}, restartRequired: false, agentConnected: true,
}
const host = { maxCpu: 12, maxRamGb: 24, maxStorageGb: 200, freeRamGb: 0, gpuAvailable: false, gpuReason: 'this execution backend has no GPU drivers installed.' }

afterEach(() => vi.unstubAllGlobals())

describe('GeneralTab', () => {
  it('shows the OS as locked with the reason, and offers no way to change it', () => {
    mockApi({})
    renderPage(<GeneralTab workspace={ws} />)
    expect(screen.getByText('Ubuntu 24.04 LTS')).toBeInTheDocument()
    expect(screen.getByText(/Locked/)).toBeInTheDocument()
    expect(screen.getByText(/Changing the operating system requires creating a new workspace/)).toBeInTheDocument()
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
  })

  it('saves only changed name and description', async () => {
    const calls = mockApi({ 'PATCH /api/workspaces/w1/settings': () => ({ ...ws, name: 'orders-v2' }) })
    renderPage(<GeneralTab workspace={ws} />)
    const save = screen.getByRole('button', { name: 'Save changes' })
    expect(save).toBeDisabled()
    await userEvent.clear(screen.getByLabelText('Workspace name'))
    await userEvent.type(screen.getByLabelText('Workspace name'), 'orders-v2')
    await userEvent.click(save)
    await waitFor(() => expect(calls.some((c) => c.method === 'PATCH')).toBe(true))
    expect(calls.find((c) => c.method === 'PATCH')?.body).toEqual({ name: 'orders-v2', description: 'Stripe webhooks' })
  })

  it('shows a name conflict next to the field', async () => {
    mockApi({ 'PATCH /api/workspaces/w1/settings': () => ({ status: 409, body: { error: { code: 'name_taken', message: 'a workspace with this name already exists', field: 'name' } } }) })
    renderPage(<GeneralTab workspace={ws} />)
    await userEvent.type(screen.getByLabelText('Workspace name'), '2')
    await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    expect(await screen.findByText(/already exists/)).toBeInTheDocument()
  })
})

describe('ResourcesTab', () => {
  const preview = (changes: unknown[], restartRequired: boolean) => () => ({ changes, restartRequired })

  it('CPU and RAM changes are reviewed and apply immediately', async () => {
    const calls = mockApi({
      'GET /api/host': () => host,
      'POST /api/workspaces/w1/settings': preview([{ field: 'cpu', from: 4, to: 6, appliesImmediately: true }], false),
      'PATCH /api/workspaces/w1/settings': () => ({ ...ws, cpu: 6 }),
    })
    renderPage(<ResourcesTab workspace={ws} />)
    const [, cpu] = screen.getAllByRole('spinbutton')
    await userEvent.clear(cpu)
    await userEvent.type(cpu, '6')
    const review = await screen.findByRole('region', { name: 'Review changes' })
    expect(await within(review).findByText(/applies immediately/)).toBeInTheDocument()
    await userEvent.click(within(review).getByRole('button', { name: 'Apply changes' }))
    await waitFor(() => expect(calls.some((c) => c.method === 'PATCH')).toBe(true))
    expect(calls.find((c) => c.method === 'PATCH')?.body).toEqual({ cpu: 6 })
  })

  it('storage growth says it needs a restart and asks for confirmation before applying', async () => {
    const calls = mockApi({
      'GET /api/host': () => host,
      'POST /api/workspaces/w1/settings': preview([{ field: 'storageGb', from: 40, to: 80, appliesImmediately: false }], true),
      'PATCH /api/workspaces/w1/settings': () => ({ ...ws, storageGb: 80, restartRequired: true }),
    })
    renderPage(<ResourcesTab workspace={ws} />)
    const storage = screen.getAllByRole('spinbutton')[2]
    await userEvent.clear(storage)
    await userEvent.type(storage, '80')
    const review = await screen.findByRole('region', { name: 'Review changes' })
    expect(await within(review).findByText(/needs restart/)).toBeInTheDocument()
    await userEvent.click(within(review).getByRole('button', { name: 'Apply and restart' }))
    expect(calls.some((c) => c.method === 'PATCH')).toBe(false)
    const dlg = screen.getByRole('dialog')
    expect(dlg).toHaveTextContent('Files, installed services and their data are kept')
    await userEvent.click(within(dlg).getByRole('button', { name: 'Restart and apply' }))
    await waitFor(() => expect(calls.find((c) => c.method === 'PATCH')?.body).toEqual({ storageGb: 80, confirmRestart: true }))
  })

  it('a rejected reduction shows the server reason and blocks apply', async () => {
    mockApi({
      'GET /api/host': () => host,
      'POST /api/workspaces/w1/settings': () => ({ status: 422, body: { error: { code: 'storage_below_usage', field: 'storageGb', message: 'Cannot reduce storage to 20 GB: 22 GB is in use. Remove data or choose at least 23 GB.' } } }),
    })
    renderPage(<ResourcesTab workspace={ws} />)
    const storage = screen.getAllByRole('spinbutton')[2]
    await userEvent.clear(storage)
    await userEvent.type(storage, '20')
    expect((await screen.findAllByText(/22 GB is in use/)).length).toBeGreaterThan(0)
    const review = screen.getByRole('region', { name: 'Review changes' })
    expect(within(review).getByRole('button', { name: /Apply/ })).toBeDisabled()
  })

  it('GPU is disabled with the reason when the backend has none', async () => {
    mockApi({ 'GET /api/host': () => host })
    renderPage(<ResourcesTab workspace={ws} />)
    await waitFor(() => expect(screen.getByText(/no GPU drivers installed/)).toBeInTheDocument())
    expect(screen.getByRole('switch')).toBeDisabled()
  })

  it('Discard restores the current values', async () => {
    mockApi({ 'GET /api/host': () => host, 'POST /api/workspaces/w1/settings': preview([{ field: 'ramGb', from: 8, to: 12, appliesImmediately: true }], false) })
    renderPage(<ResourcesTab workspace={ws} />)
    const ram = screen.getAllByRole('spinbutton')[0]
    await userEvent.clear(ram)
    await userEvent.type(ram, '12')
    await userEvent.click(await screen.findByRole('button', { name: 'Discard' }))
    expect(screen.getAllByRole('spinbutton')[0]).toHaveValue(8)
    expect(screen.queryByRole('region', { name: 'Review changes' })).not.toBeInTheDocument()
  })
})
