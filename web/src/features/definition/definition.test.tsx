import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { DefinitionTab } from './DefinitionTab'
import { ImportDefinitionDialog } from './ImportDialog'
import { mockApi, renderPage } from '@/test/utils'
import type { Workspace } from '@/api/types'

const ws = { id: 'ws1', name: 'dev', status: 'ready' } as unknown as Workspace
const err = (code: string, message: string, extra = {}) => ({ status: 422, body: { error: { code, message, ...extra } } })

describe('DefinitionTab', () => {
  it('offers YAML and JSON exports', () => {
    mockApi({})
    renderPage(<DefinitionTab workspace={ws} />)
    expect(screen.getByRole('link', { name: 'Download YAML' })).toHaveAttribute('href', '/api/workspaces/ws1/definition')
    expect(screen.getByRole('link', { name: 'Download JSON' })).toHaveAttribute('href', '/api/workspaces/ws1/definition?format=json')
    expect(screen.getByText(/never contains secret values/)).toBeInTheDocument()
  })

  it('lists every difference grouped by category', async () => {
    const calls = mockApi({
      'POST /api/workspaces/ws1/drift': () => ({
        inSync: false,
        differences: [
          { category: 'workspace', subject: 'resources', field: 'cpu', expected: '2', actual: '4', kind: 'changed' },
          { category: 'variable', subject: 'EXTRA_FLAG', expected: 'not in the definition', actual: 'set', kind: 'extra' },
          { category: 'component', subject: 'redis', field: 'desired state', expected: 'running', actual: 'stopped', kind: 'changed' },
        ],
      }),
    })
    renderPage(<DefinitionTab workspace={ws} />)
    const btn = screen.getByRole('button', { name: 'Check for drift' })
    expect(btn).toBeDisabled()
    await userEvent.type(screen.getByRole('textbox', { name: 'Definition' }), 'kind: NexmeshWorkspace')
    await userEvent.click(btn)
    expect(await screen.findByText('3 differences from the definition')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Workspace' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Variables' })).toBeInTheDocument()
    const row = screen.getByText('EXTRA_FLAG').closest('li')!
    expect(within(row).getByText('Extra')).toBeInTheDocument()
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ definition: 'kind: NexmeshWorkspace' })
  })

  it('says when the workspace is in sync and shows unreadable files as errors', async () => {
    let inSync = true
    mockApi({ 'POST /api/workspaces/ws1/drift': () => (inSync ? { inSync: true, differences: [] } : err('unreadable_definition', 'This is not a Nexmesh workspace definition.')) })
    renderPage(<DefinitionTab workspace={ws} />)
    await userEvent.type(screen.getByRole('textbox', { name: 'Definition' }), 'x')
    await userEvent.click(screen.getByRole('button', { name: 'Check for drift' }))
    expect(await screen.findByText(/In sync/)).toBeInTheDocument()
    inSync = false
    await userEvent.type(screen.getByRole('textbox', { name: 'Definition' }), 'y')
    await userEvent.click(screen.getByRole('button', { name: 'Check for drift' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('not a Nexmesh workspace definition')
  })
})

describe('ImportDefinitionDialog', () => {
  it('requires a successful check before creating, then lists what the user must supply', async () => {
    const calls = mockApi({
      'POST /api/workspaces/import': (_u, init) => {
        const b = JSON.parse(String(init?.body))
        const todos = [{ kind: 'credential', subject: 'prod-db (MongoDB)', detail: 'Enter the password on the Connect tab and test the connection.' }, { kind: 'secret_variable', subject: 'STRIPE_KEY', detail: 'Add the value under Variables.' }]
        return b.dryRun ? { dryRun: true, todos } : { status: 201, body: { dryRun: false, todos, workspace: { id: 'ws9', name: 'copy' } } }
      },
    })
    renderPage(<ImportDefinitionDialog onClose={() => {}} />)
    const create = screen.getByRole('button', { name: 'Create workspace' })
    expect(create).toBeDisabled()
    await userEvent.type(screen.getByLabelText('Definition file'), 'schemaVersion: 1')
    expect(create).toBeDisabled() // not checked yet
    await userEvent.type(screen.getByLabelText(/Workspace name/), 'COPY')
    await userEvent.click(screen.getByLabelText(/Install the services and runtimes automatically/))
    await userEvent.click(screen.getByRole('button', { name: 'Check' }))
    expect(await screen.findByText(/The definition is valid/)).toBeInTheDocument()
    expect(screen.getByText(/supply 2 items afterwards/)).toBeInTheDocument()
    await userEvent.click(create)
    expect(await screen.findByText(/is provisioning/)).toBeInTheDocument()
    expect(screen.getByText('Connection credentials')).toBeInTheDocument()
    expect(screen.getByText('STRIPE_KEY')).toBeInTheDocument()
    expect(calls.map((c) => c.body)).toEqual([
      { definition: 'schemaVersion: 1', name: 'copy', autoInstall: true, dryRun: true },
      { definition: 'schemaVersion: 1', name: 'copy', autoInstall: true, dryRun: false },
    ])
  })

  it('shows each validation issue and invalidates a previous check when the file changes', async () => {
    let bad = true
    mockApi({
      'POST /api/workspaces/import': () => (bad
        ? err('invalid_definition', '2 problems', { issues: [{ path: 'components[1].connection.password', message: 'this file contains a value for the credential "password".' }, { path: 'components[0].version', message: 'Redis has no version "0.0.1".' }] })
        : { dryRun: true, todos: [] }),
    })
    renderPage(<ImportDefinitionDialog onClose={() => {}} />)
    await userEvent.type(screen.getByLabelText('Definition file'), 'x')
    await userEvent.click(screen.getByRole('button', { name: 'Check' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent("can't be imported yet")
    expect(within(alert).getByText('components[1].connection.password')).toBeInTheDocument()
    expect(within(alert).getByText(/Redis has no version/)).toBeInTheDocument()
    bad = false
    await userEvent.type(screen.getByLabelText('Definition file'), 'y')
    expect(screen.queryByRole('alert')).toBeNull() // editing clears the previous result
    await userEvent.click(screen.getByRole('button', { name: 'Check' }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Create workspace' })).toBeEnabled())
  })

  it('reports a taken name on the name field', async () => {
    mockApi({ 'POST /api/workspaces/import': () => ({ status: 409, body: { error: { code: 'name_taken', message: 'a workspace with this name already exists', field: 'name' } } }) })
    renderPage(<ImportDefinitionDialog onClose={() => {}} />)
    await userEvent.type(screen.getByLabelText('Definition file'), 'x')
    await userEvent.click(screen.getByRole('button', { name: 'Check' }))
    expect(await screen.findByText('a workspace with this name already exists')).toBeInTheDocument()
  })
})
