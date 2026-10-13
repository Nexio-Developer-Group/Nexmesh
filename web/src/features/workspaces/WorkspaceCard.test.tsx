import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { WorkspaceCard } from './WorkspaceCard'
import type { Workspace } from '@/api/types'

const base: Workspace = {
  id: 'ws_1', name: 'orders-api', description: '', os: 'ubuntu-24.04', cpu: 4, ramGb: 8, storageGb: 40, gpu: false,
  status: 'ready', step: 'ready', createdAt: '2026-10-13T10:00:00Z', lastOpenedAt: '2026-10-13T10:00:00Z',
  runtimes: {}, restartRequired: false, agentConnected: false,
}
const noop = () => {}
const renderCard = (w: Partial<Workspace>, handlers = {}) =>
  render(<MemoryRouter><WorkspaceCard workspace={{ ...base, ...w }} view="grid" onStart={noop} onStop={noop} onDelete={noop} onViewError={noop} {...handlers} /></MemoryRouter>)

describe('WorkspaceCard', () => {
  it('ready workspace shows Running and an Open link', () => {
    renderCard({})
    expect(screen.getByText('Running')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Open' })).toHaveAttribute('href', '/w/ws_1')
    expect(screen.getByText('Ubuntu 24.04 LTS')).toBeInTheDocument()
  })

  it('provisioning shows the real step and progress', () => {
    renderCard({ status: 'preparing', step: 'preparing' })
    expect(screen.getByText('Preparing environment')).toBeInTheDocument()
    expect(screen.getByText('2 / 4')).toBeInTheDocument()
    expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '2')
  })

  it('failed shows the reason and a View error action', async () => {
    const onViewError = vi.fn()
    renderCard({ status: 'failed', error: 'requested 16 GB RAM, host has 11.2 GB free' }, { onViewError })
    expect(screen.getByText(/11.2 GB free/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'View error' }))
    expect(onViewError).toHaveBeenCalled()
  })

  it('stopped offers Start', async () => {
    const onStart = vi.fn()
    renderCard({ status: 'stopped' }, { onStart })
    await userEvent.click(screen.getByRole('button', { name: 'Start' }))
    expect(onStart).toHaveBeenCalled()
  })

  it('delete is reached through the actions menu', async () => {
    const onDelete = vi.fn()
    renderCard({}, { onDelete })
    await userEvent.click(screen.getByRole('button', { name: 'Actions for orders-api' }))
    await userEvent.click(screen.getByRole('menuitem', { name: 'Delete…' }))
    expect(onDelete).toHaveBeenCalled()
  })
})
