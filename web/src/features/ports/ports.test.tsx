import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { PortsPanel } from './PortsPanel'
import { mockApi, renderPage } from '@/test/utils'

const listing = (over = {}) => ({
  online: true, previewMode: 'path',
  ports: [
    { port: 3000, declared: true, label: 'web', detected: true, local: true, process: 'node' },
    { port: 8080, declared: false, detected: true, local: false },
    { port: 5173, declared: true, label: 'storybook', detected: false, local: false },
  ],
  ...over,
})

describe('PortsPanel', () => {
  it('lists detected and declared ports with their state', async () => {
    mockApi({ 'GET /api/workspaces/ws1/ports': () => listing() })
    renderPage(<PortsPanel workspaceId="ws1" />)
    const web = (await screen.findByText('web')).closest('li')!
    expect(within(web).getByText('Listening')).toBeInTheDocument()
    expect(within(web).getByText(/node · loopback only/)).toBeInTheDocument()
    const sb = screen.getByText('storybook').closest('li')!
    expect(within(sb).getByText('Not running')).toBeInTheDocument()
    expect(within(sb).getByRole('button', { name: 'Open preview of port 5173' })).toBeDisabled()
    expect(screen.getByText(/sandbox/)).toBeInTheDocument() // path-mode notice
  })

  it('requests a preview URL and navigates the opened tab to it', async () => {
    const fake = { opener: 'x', location: { href: '' }, close: vi.fn() }
    const open = vi.spyOn(window, 'open').mockReturnValue(fake as unknown as Window)
    const calls = mockApi({
      'GET /api/workspaces/ws1/ports': () => listing(),
      'POST /api/workspaces/ws1/ports/3000/preview': () => ({ url: '/api/preview/ws1/3000/', expiresAt: '', mode: 'path' }),
    })
    renderPage(<PortsPanel workspaceId="ws1" />)
    await userEvent.click(await screen.findByRole('button', { name: 'Open preview of port 3000' }))
    await waitFor(() => expect(fake.location.href).toBe('/api/preview/ws1/3000/'))
    expect(fake.opener).toBeNull()
    expect(calls.some((c) => c.url.endsWith('/ports/3000/preview'))).toBe(true)
    open.mockRestore()
  })

  it('declares, renames and removes ports', async () => {
    const calls = mockApi({
      'GET /api/workspaces/ws1/ports': () => listing(),
      'PUT /api/workspaces/ws1/ports/': () => ({ status: 204 }),
      'DELETE /api/workspaces/ws1/ports/': () => ({ status: 204 }),
    })
    renderPage(<PortsPanel workspaceId="ws1" />)
    await userEvent.click(await screen.findByRole('button', { name: 'Add port' }))
    const save = screen.getByRole('button', { name: 'Save' })
    expect(save).toBeDisabled()
    await userEvent.type(screen.getByLabelText('Port'), '4000')
    await userEvent.type(screen.getByLabelText(/Label/), 'api')
    await userEvent.click(save)
    await waitFor(() => expect(calls.find((c) => c.method === 'PUT')).toMatchObject({ url: '/api/workspaces/ws1/ports/4000', body: { label: 'api' } }))
    await userEvent.click(screen.getByRole('button', { name: 'Remove port 3000' }))
    await waitFor(() => expect(calls.find((c) => c.method === 'DELETE')?.url).toBe('/api/workspaces/ws1/ports/3000'))
  })

  it('explains when the workspace agent is offline and when nothing listens', async () => {
    mockApi({ 'GET /api/workspaces/ws1/ports': () => listing({ online: false, ports: [] }) })
    renderPage(<PortsPanel workspaceId="ws1" />)
    expect(await screen.findByText(/not connected/)).toBeInTheDocument()
  })
})
