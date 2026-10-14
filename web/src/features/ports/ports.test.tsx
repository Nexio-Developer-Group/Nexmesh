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

describe('Share links', () => {
  const links = (enabled: boolean, active = true) => ({ enabled, links: active ? [{ id: 'shr_1', port: 3000, label: 'Demo', createdAt: '', expiresAt: '2030-01-01T00:00:00Z', revoked: false, active: true, hits: 2 }] : [] })

  it('hides sharing when the instance has it off', async () => {
    mockApi({ 'GET /api/workspaces/ws1/ports': () => listing(), 'GET /api/workspaces/ws1/shares': () => links(false, false) })
    renderPage(<PortsPanel workspaceId="ws1" />)
    expect(await screen.findByText(/share links are turned off/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Share port/ })).toBeNull()
  })

  it('creates a link with an expiry, shows the URL once and lists it for revocation', async () => {
    const calls = mockApi({
      'GET /api/workspaces/ws1/ports': () => listing(),
      'GET /api/workspaces/ws1/shares': () => links(true),
      'POST /api/workspaces/ws1/shares': () => ({ status: 201, body: { link: { id: 'shr_2', port: 3000 }, url: 'http://x/share/ws1/3000/?nx_share=shr_2.secret' } }),
      'DELETE /api/workspaces/ws1/shares/': () => ({ status: 204 }),
    })
    renderPage(<PortsPanel workspaceId="ws1" />)
    expect(screen.queryByRole('button', { name: 'Share port 8080' })).toBeNull()
    await userEvent.click(await screen.findByRole('button', { name: 'Share port 3000' }))
    expect(screen.getByText(/Anyone who has the link/)).toBeInTheDocument()
    await userEvent.selectOptions(screen.getByLabelText('Link expires after'), '1440')
    await userEvent.click(screen.getByRole('button', { name: 'Create link' }))
    expect(await screen.findByLabelText('Share link')).toHaveValue('http://x/share/ws1/3000/?nx_share=shr_2.secret')
    expect(screen.getByText(/won't be shown again/)).toBeInTheDocument()
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ port: 3000, ttlMinutes: 1440, label: '' })
    await userEvent.click(screen.getByRole('button', { name: 'Done' }))

    const section = screen.getByRole('region', { name: 'Shared links' })
    expect(within(section).getByText(/opened 2×/)).toBeInTheDocument()
    await userEvent.click(within(section).getByRole('button', { name: 'Revoke link for port 3000' }))
    await waitFor(() => expect(calls.find((c) => c.method === 'DELETE')?.url).toBe('/api/workspaces/ws1/shares/shr_1'))
  })
})
