import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { EnvironmentSwitcher } from './EnvironmentSwitcher'
import { mockApi, renderPage } from '@/test/utils'
import type { Workspace } from '@/api/types'

const ws = (extra: Partial<Workspace> = {}) => ({ id: 'ws', name: 'w', status: 'ready', ...extra }) as unknown as Workspace

describe('EnvironmentSwitcher', () => {
  it('says nothing is chosen yet and switches to development or staging in one click', async () => {
    const calls = mockApi({ 'POST /api/workspaces/ws/environment': () => ({ ...ws(), activeEnvironment: 'staging' }) })
    renderPage(<EnvironmentSwitcher workspace={ws()} />)
    const group = screen.getByRole('radiogroup', { name: 'Active environment' })
    expect(group.querySelectorAll('[aria-checked="true"]')).toHaveLength(0)
    await userEvent.click(screen.getByRole('radio', { name: 'Staging' }))
    await waitFor(() => expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ environment: 'staging' }))
  })

  it('marks the active environment, and production needs an explicit confirmation', async () => {
    const calls = mockApi({ 'POST /api/workspaces/ws/environment': () => ({ ...ws(), activeEnvironment: 'production' }) })
    renderPage(<EnvironmentSwitcher workspace={ws({ activeEnvironment: 'development' })} />)
    expect(screen.getByRole('radio', { name: 'Development' })).toBeChecked()
    await userEvent.click(screen.getByRole('radio', { name: 'Production' }))
    expect(calls.some((c) => c.method === 'POST')).toBe(false) // nothing changes until confirmed
    const dialog = screen.getByRole('dialog', { name: 'Switch to Production?' })
    expect(dialog).toHaveTextContent('read-only by default')
    await userEvent.click(screen.getByRole('button', { name: 'Switch to Production' }))
    await waitFor(() => expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ environment: 'production', confirm: true }))
  })

  it('shows production distinctly with a symbol, not colour alone', () => {
    renderPage(<EnvironmentSwitcher workspace={ws({ activeEnvironment: 'production' })} />)
    const prod = screen.getByRole('radio', { name: /Production/ })
    expect(prod).toBeChecked()
    expect(prod).toHaveTextContent('▲')
  })

  it('cancelling the production confirmation changes nothing', async () => {
    const calls = mockApi({})
    renderPage(<EnvironmentSwitcher workspace={ws({ activeEnvironment: 'staging' })} />)
    await userEvent.click(screen.getByRole('radio', { name: 'Production' }))
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(calls.some((c) => c.method === 'POST')).toBe(false)
  })
})
