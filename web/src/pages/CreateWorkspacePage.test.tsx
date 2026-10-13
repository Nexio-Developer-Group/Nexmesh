import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import CreateWorkspacePage from './CreateWorkspacePage'
import { mockApi, renderPage } from '@/test/utils'

const host = { maxCpu: 12, maxRamGb: 24, maxStorageGb: 200, freeRamGb: 0, gpuAvailable: false, gpuReason: 'this execution backend has no GPU drivers installed.' }

afterEach(() => vi.unstubAllGlobals())

describe('CreateWorkspacePage', () => {
  it('shows the locked OS and a disabled GPU switch with the reason', async () => {
    mockApi({ 'GET /api/host': () => host })
    renderPage(<CreateWorkspacePage />)
    expect(screen.getByText('Ubuntu 24.04 LTS', { selector: 'p' })).toBeInTheDocument()
    expect(screen.getByText(/Fixed after creation/)).toBeInTheDocument()
    await waitFor(() => expect(screen.getByText(/no GPU drivers installed/)).toBeInTheDocument())
    expect(screen.getByRole('switch')).toBeDisabled()
  })

  it('explains the limit and blocks creation when RAM exceeds the host limit', async () => {
    mockApi({ 'GET /api/host': () => host, 'GET /api/workspaces/name-available': () => ({ available: true }) })
    renderPage(<CreateWorkspacePage />)
    await screen.findByText(/Per-workspace limit/)
    await userEvent.type(screen.getByLabelText('Workspace name'), 'payments')
    const ram = screen.getAllByRole('spinbutton')[0]
    await userEvent.clear(ram)
    await userEvent.type(ram, '25')
    expect(await screen.findAllByText(/RAM must be between 1 and 24 GB/)).not.toHaveLength(0)
    expect(screen.getByRole('button', { name: 'Create Code Space' })).toBeDisabled()
  })

  it('submits the configuration and navigates to provisioning', async () => {
    const calls = mockApi({
      'GET /api/host': () => host,
      'GET /api/workspaces/name-available': () => ({ available: true }),
      'POST /api/workspaces': () => ({ id: 'ws_1', name: 'payments' }),
    })
    renderPage(<CreateWorkspacePage />)
    await screen.findByText(/Per-workspace limit/)
    await userEvent.type(screen.getByLabelText('Workspace name'), 'payments')
    await screen.findByText('Available')
    await userEvent.click(screen.getByRole('button', { name: 'Create Code Space' }))
    await waitFor(() => expect(screen.getByText('elsewhere')).toBeInTheDocument())
    const post = calls.find((c) => c.method === 'POST')
    expect(post?.body).toMatchObject({ name: 'payments', cpu: 4, ramGb: 8, storageGb: 40, gpu: false })
  })

  it('rejects invalid names inline', async () => {
    mockApi({ 'GET /api/host': () => host })
    renderPage(<CreateWorkspacePage />)
    await userEvent.type(screen.getByLabelText('Workspace name'), '-bad')
    expect(screen.getByText(/lowercase letters, digits or hyphens/)).toBeInTheDocument()
  })
})
