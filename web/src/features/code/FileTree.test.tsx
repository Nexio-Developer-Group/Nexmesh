import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { FileTree } from './FileTree'
import { mockApi, renderPage } from '@/test/utils'

const e = (path: string, type: 'file' | 'dir' = 'file') => ({ name: path.split('/').pop()!, path, type, size: 1, modTime: '2026-01-01T00:00:00Z', mode: '-rw-r--r--' })

function setup(extra: Record<string, (url: string, init?: RequestInit) => unknown> = {}) {
  const onOpen = vi.fn()
  const onPathChanged = vi.fn()
  const calls = mockApi({
    'GET /api/workspaces/ws1/files/search': (url) => ({ hits: url.includes('q=main') ? [{ path: 'src/main.go', line: 3, text: '  func main() {' }] : [], truncated: false }),
    'GET /api/workspaces/ws1/files?path=src': () => ({ entries: [e('src/main.go')] }),
    'GET /api/workspaces/ws1/files?path=': () => ({ entries: [e('src', 'dir'), e('README.md')] }),
    ...extra,
  })
  renderPage(<FileTree workspaceId="ws1" activePath={null} onOpen={onOpen} onPathChanged={onPathChanged} />)
  return { calls, onOpen, onPathChanged }
}

describe('FileTree', () => {
  it('lists the root, expands folders lazily and opens files', async () => {
    const { calls, onOpen } = setup()
    expect(await screen.findByText('README.md')).toBeInTheDocument()
    expect(calls.some((c) => c.url.includes('path=src'))).toBe(false)
    await userEvent.click(screen.getByText('src'))
    expect(await screen.findByText('main.go')).toBeInTheDocument()
    await userEvent.click(screen.getByText('main.go'))
    expect(onOpen).toHaveBeenCalledWith('src/main.go')
    expect(screen.getByRole('treeitem', { name: /src/ })).toHaveAttribute('aria-expanded', 'true')
  })

  it('creates a file inside the selected folder and opens it', async () => {
    const { calls, onOpen } = setup({ 'PUT /api/workspaces/ws1/files/content': () => ({ size: 0, modTime: '' }) })
    await userEvent.click(await screen.findByText('src'))
    await userEvent.click(await screen.findByRole('button', { name: 'New file' }))
    await userEvent.type(screen.getByLabelText('Name'), 'util.go')
    await userEvent.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(onOpen).toHaveBeenCalledWith('src/util.go'))
    expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({ path: 'src/util.go', content: '' })
  })

  it('renames/moves with the path field and reports the change', async () => {
    const { calls, onPathChanged } = setup({ 'POST /api/workspaces/ws1/files/rename': () => ({ status: 204 }) })
    await userEvent.click(await screen.findByRole('button', { name: 'Actions for README.md' }))
    await userEvent.click(screen.getByRole('menuitem', { name: 'Rename / move' }))
    const field = screen.getByLabelText('Path')
    expect(field).toHaveValue('README.md')
    await userEvent.clear(field)
    await userEvent.type(field, 'docs/README.md')
    await userEvent.click(screen.getByRole('button', { name: 'Rename' }))
    await waitFor(() => expect(onPathChanged).toHaveBeenCalledWith('README.md', 'docs/README.md'))
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ from: 'README.md', to: 'docs/README.md' })
  })

  it('asks before deleting and deletes folders recursively', async () => {
    const { calls, onPathChanged } = setup({ 'DELETE /api/workspaces/ws1/files': () => ({ status: 204 }) })
    await userEvent.click(await screen.findByRole('button', { name: 'Actions for src' }))
    await userEvent.click(screen.getByRole('menuitem', { name: 'Delete' }))
    const dlg = screen.getByRole('dialog')
    expect(within(dlg).getByText(/everything inside it/)).toBeInTheDocument()
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false)
    await userEvent.click(within(dlg).getByRole('button', { name: 'Delete folder' }))
    await waitFor(() => expect(onPathChanged).toHaveBeenCalledWith('src', null))
    expect(calls.find((c) => c.method === 'DELETE')?.url).toContain('recursive=true')
  })

  it('searches names and content and opens a hit at its line', async () => {
    const { onOpen } = setup()
    await screen.findByText('README.md')
    await userEvent.type(screen.getByLabelText('Search files'), 'main')
    const hit = await screen.findByRole('button', { name: /src\/main.go:3/ })
    await userEvent.click(hit)
    expect(onOpen).toHaveBeenCalledWith('src/main.go', 3)
  })

  it('shows a friendly message when the agent is unreachable', async () => {
    mockApi({ 'GET /api/workspaces/ws1/files': () => ({ status: 409, body: { error: { code: 'agent_offline', message: 'The workspace agent is not connected yet. Try again in a moment.' } } }) })
    renderPage(<FileTree workspaceId="ws1" activePath={null} onOpen={() => {}} onPathChanged={() => {}} />)
    expect(await screen.findByRole('alert')).toHaveTextContent('not connected yet')
  })
})
