import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { DefinitionTab } from './DefinitionTab'
import { ImportDevContainerDialog } from './ImportDevContainerDialog'
import { mockApi, renderPage } from '@/test/utils'
import type { Workspace } from '@/api/types'

const report = [
  { field: 'name', status: 'adapted', detail: 'Workspace name "My App!" became "my-app".' },
  { field: 'forwardPorts', status: 'applied', detail: 'Port 3000 is declared.' },
  { field: 'remoteUser', status: 'unsupported', detail: 'Workspaces run as the default user.' },
]
const checked = { dryRun: true, todos: [], report, postCreate: ['npm install', 'make setup'], name: 'my-app', resources: { cpu: 2, ramGb: 4, storageGb: 20 } }

describe('ImportDevContainerDialog', () => {
  it('shows what was applied, adapted and ignored before creating, and the commands that would run', async () => {
    const calls = mockApi({
      'POST /api/devcontainer/import': (_u, init) => {
        const b = JSON.parse(String(init?.body))
        return b.dryRun ? checked : { status: 201, body: { ...checked, dryRun: false, workspace: { id: 'ws9', name: 'my-app' } } }
      },
    })
    renderPage(<ImportDevContainerDialog onClose={() => {}} />)
    const create = screen.getByRole('button', { name: 'Create workspace' })
    expect(create).toBeDisabled()
    await userEvent.type(screen.getByLabelText('devcontainer.json'), '{{}')
    await userEvent.click(screen.getByLabelText(/Run the post-create commands/))
    await userEvent.click(screen.getByRole('button', { name: 'Check' }))
    expect((await screen.findByText(/Ready to create/)).closest('p')).toHaveTextContent('2 vCPU, 4 GB RAM and 20 GB storage on Ubuntu 24.04 LTS')
    const list = screen.getByRole('region', { name: 'Compatibility report' })
    expect(within(list).getByText('1 applied · 1 adapted · 1 not supported')).toBeInTheDocument()
    expect(within(list).getByText('remoteUser')).toBeInTheDocument()
    expect(within(list).getByText('Not supported')).toBeInTheDocument()
    expect(screen.getByText('$ npm install')).toBeInTheDocument()
    await userEvent.click(create)
    expect(await screen.findByText(/is provisioning from your dev container file/)).toHaveTextContent('Post-create commands run once it is ready')
    expect(calls.map((c) => (c.body as { dryRun: boolean; runPostCreate: boolean }).dryRun)).toEqual([true, false])
    expect((calls[1].body as { runPostCreate: boolean }).runPostCreate).toBe(true)
  })

  it('keeps post-create commands off by default and shows unreadable files clearly', async () => {
    const calls = mockApi({ 'POST /api/devcontainer/import': () => ({ status: 422, body: { error: { code: 'unreadable_devcontainer', message: 'This is not valid devcontainer.json (syntax error at byte 3).' } } }) })
    renderPage(<ImportDevContainerDialog onClose={() => {}} />)
    expect(screen.getByLabelText(/Run the post-create commands/)).not.toBeChecked()
    await userEvent.type(screen.getByLabelText('devcontainer.json'), 'nope')
    await userEvent.click(screen.getByRole('button', { name: 'Check' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('not valid devcontainer.json')
    expect((calls[0].body as { runPostCreate: boolean }).runPostCreate).toBe(false)
  })
})

describe('Dev container export', () => {
  const ws = { id: 'ws1', name: 'dev', status: 'ready' } as unknown as Workspace
  it('offers the download and a compatibility report on demand', async () => {
    const calls = mockApi({ 'GET /api/workspaces/ws1/devcontainer': () => ({ report: [{ field: 'variables.STRIPE_KEY', status: 'unsupported', detail: 'Secret values are never exported.' }, { field: 'image', status: 'applied', detail: 'Ubuntu 24.04 base image.' }], file: '{}' }) })
    renderPage(<DefinitionTab workspace={ws} />)
    expect(screen.getByRole('link', { name: 'Download devcontainer.json' })).toHaveAttribute('href', '/api/workspaces/ws1/devcontainer')
    expect(calls.some((c) => c.url.includes('devcontainer'))).toBe(false)
    await userEvent.click(screen.getByRole('button', { name: 'Show compatibility report' }))
    const rep = await screen.findByRole('region', { name: 'Export compatibility report' })
    expect(within(rep).getByText('variables.STRIPE_KEY')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByRole('button', { name: 'Hide compatibility report' })).toBeInTheDocument())
  })
})
