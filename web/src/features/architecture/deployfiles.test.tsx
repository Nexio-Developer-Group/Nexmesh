import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { DeployFiles } from './DeployFiles'
import { mockApi, renderPage } from '@/test/utils'

const plan = {
  files: [
    { path: 'docker-compose.yml', content: 'services: {}', exists: false, ours: false },
    { path: 'api/Dockerfile', content: 'FROM node', exists: true, ours: false },
    { path: '.env.example', content: 'REDIS_URI=\n', exists: true, ours: true },
  ],
  notes: ['React is a frontend with no project in the workspace.'],
}

describe('DeployFiles', () => {
  it('lists files with their status, selects only new ones by default, and writes after confirmation', async () => {
    const calls = mockApi({
      'GET /api/workspaces/ws/export/compose': () => plan,
      'POST /api/workspaces/ws/export/compose/write': () => ({ written: ['docker-compose.yml'], skipped: [] }),
    })
    renderPage(<DeployFiles workspaceId="ws" />)
    expect(await screen.findByText('docker-compose.yml', { selector: 'span' })).toBeInTheDocument()
    expect(screen.getByText('Exists (yours)')).toBeInTheDocument()
    expect(screen.getByText('Exists (generated earlier)')).toBeInTheDocument()
    expect(screen.getByText(/no project in the workspace/)).toBeInTheDocument()
    expect(screen.getByRole('checkbox', { name: 'docker-compose.yml' })).toBeChecked()
    expect(screen.getByRole('checkbox', { name: 'api/Dockerfile' })).not.toBeChecked()
    await userEvent.click(screen.getByRole('button', { name: 'Write 1 file to the workspace' }))
    await waitFor(() => expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ paths: ['docker-compose.yml'], overwrite: false, confirm: true }))
    expect(await screen.findByRole('status')).toHaveTextContent('Wrote 1 file')
  })

  it('offers to replace existing files only when one is selected, and sends the choice', async () => {
    const calls = mockApi({
      'GET /api/workspaces/ws/export/compose': () => plan,
      'POST /api/workspaces/ws/export/compose/write': () => ({ written: [], skipped: [] }),
    })
    renderPage(<DeployFiles workspaceId="ws" />)
    await screen.findByText('api/Dockerfile')
    expect(screen.queryByLabelText(/Replace files that already exist/)).toBeNull()
    await userEvent.click(screen.getByRole('checkbox', { name: 'api/Dockerfile' }))
    await userEvent.click(screen.getByLabelText(/Replace files that already exist/))
    await userEvent.click(screen.getByRole('button', { name: /Write 2 files/ }))
    await waitFor(() => expect(calls.find((c) => c.method === 'POST')?.body).toMatchObject({ overwrite: true, paths: ['docker-compose.yml', 'api/Dockerfile'] }))
  })

  it('previews the content and explains when the workspace cannot be read', async () => {
    mockApi({ 'GET /api/workspaces/ws/export/compose': () => plan })
    const { unmount } = renderPage(<DeployFiles workspaceId="ws" />)
    const item = (await screen.findByText('api/Dockerfile')).closest('li')!
    expect(within(item).getByText('FROM node')).toBeInTheDocument()
    unmount()
    mockApi({ 'GET /api/workspaces/ws/export/compose': () => ({ status: 409, body: { error: { code: 'agent_offline', message: 'The workspace agent is not connected yet.' } } }) })
    renderPage(<DeployFiles workspaceId="ws" />)
    expect(await screen.findByRole('alert')).toHaveTextContent('not connected yet')
  })
})
