import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { GitCredentialsTab } from './GitCredentialsTab'
import { GitPanel } from './GitPanel'
import { mockApi, renderPage } from '@/test/utils'

const status = (over = {}) => ({
  repo: 'app', branch: 'main', detached: false, upstream: 'origin/main', ahead: 1, behind: 0, clean: false,
  files: [
    { path: 'staged.go', index: 'A', worktree: ' ', staged: true, unstaged: false, untracked: false, conflicted: false },
    { path: 'edited.go', index: ' ', worktree: 'M', staged: false, unstaged: true, untracked: false, conflicted: false },
    { path: 'new.txt', index: '?', worktree: '?', staged: false, unstaged: false, untracked: true, conflicted: false },
  ],
  ...over,
})

function panel(extra: Record<string, (u: string, i?: RequestInit) => unknown> = {}) {
  const onOpenFile = vi.fn()
  const calls = mockApi({
    'GET /api/workspaces/ws1/git/repos': () => ({ repos: ['app'] }),
    'GET /api/workspaces/ws1/git/status': () => status(),
    ...extra,
  })
  renderPage(<GitPanel workspaceId="ws1" onOpenFile={onOpenFile} />)
  return { calls, onOpenFile }
}

describe('GitPanel', () => {
  it('shows branch, ahead/behind and grouped changes', async () => {
    panel()
    expect(await screen.findByRole('button', { name: /main/ })).toBeInTheDocument()
    expect(screen.getByLabelText('1 ahead, 0 behind')).toBeInTheDocument()
    expect(within(screen.getByRole('region', { name: 'Staged changes' })).getByText('staged.go')).toBeInTheDocument()
    expect(within(screen.getByRole('region', { name: 'Changes' })).getByText('edited.go')).toBeInTheDocument()
    expect(within(screen.getByRole('region', { name: 'Untracked' })).getByText('new.txt')).toBeInTheDocument()
  })

  it('says a commit will be marked when staged files were written by the Code Agent', async () => {
    panel({ 'GET /api/workspaces/ws1/git/status': () => status({ agentFiles: ['staged.go'] }) })
    expect(await screen.findByRole('note')).toHaveTextContent('Assisted-by: Nexmesh Code Agent')
  })

  it('shows no agent note when none of the staged files came from the agent', async () => {
    panel({ 'GET /api/workspaces/ws1/git/status': () => status({ agentFiles: ['edited.go'] }) })
    await screen.findByRole('region', { name: 'Staged changes' })
    expect(screen.queryByRole('note')).toBeNull()
  })

  it('stages and unstages files', async () => {
    const { calls } = panel({ 'POST /api/workspaces/ws1/git/stage': () => ({ status: 204 }) })
    await userEvent.click(await screen.findByRole('button', { name: 'Stage edited.go' }))
    await userEvent.click(screen.getByRole('button', { name: 'Unstage staged.go' }))
    await userEvent.click(within(screen.getByRole('region', { name: 'Untracked' })).getByRole('button', { name: 'Stage all' }))
    const posts = calls.filter((c) => c.method === 'POST').map((c) => c.body)
    expect(posts[0]).toEqual({ repo: 'app', paths: ['edited.go'] })
    expect(posts[1]).toEqual({ repo: 'app', paths: ['staged.go'], unstage: true })
    expect(posts[2]).toEqual({ repo: 'app', paths: ['new.txt'] })
  })

  it('commits only with staged files and a message, then clears the box', async () => {
    const { calls } = panel({ 'POST /api/workspaces/ws1/git/commit': () => ({ status: 204 }) })
    const btn = await screen.findByRole('button', { name: /Commit 1 file/ })
    expect(btn).toBeDisabled()
    await userEvent.type(screen.getByLabelText('Commit message'), 'fix the bug')
    expect(btn).toBeEnabled()
    await userEvent.click(btn)
    await waitFor(() => expect(screen.getByLabelText('Commit message')).toHaveValue(''))
    expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ repo: 'app', message: 'fix the bug' })
  })

  it('explains an auth failure with a link to credentials and the raw details', async () => {
    panel({ 'POST /api/workspaces/ws1/git/push': () => ({ status: 502, body: { error: { code: 'auth_failed', message: 'Git rejected the credentials. Add or replace the access token under Git credentials.', detail: 'fatal: Authentication failed for https://github.com/acme/app.git/' } } }) })
    await userEvent.click(await screen.findByRole('button', { name: 'Push' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Git rejected the credentials')
    expect(within(alert).getByRole('link', { name: 'Open Git credentials' })).toHaveAttribute('href', '/w/ws1/settings?tab=git')
    await userEvent.click(within(alert).getByText('Details'))
    expect(within(alert).getByText(/Authentication failed/)).toBeVisible()
  })

  it('opens the pull request link in a new tab safely', async () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    panel({ 'GET /api/workspaces/ws1/git/pr-url': () => ({ url: 'https://github.com/acme/app/compare/main?expand=1' }) })
    await userEvent.click(await screen.findByRole('button', { name: /Open pull request/ }))
    await waitFor(() => expect(open).toHaveBeenCalledWith('https://github.com/acme/app/compare/main?expand=1', '_blank', 'noopener,noreferrer'))
    open.mockRestore()
  })

  it('creates a branch and requires confirmation to discard', async () => {
    const { calls } = panel({
      'GET /api/workspaces/ws1/git/branches': () => ({ branches: [{ name: 'main', current: true }, { name: 'dev', current: false }, { name: 'origin/main', current: false, remote: true }, { name: 'origin/hotfix', current: false, remote: true }] }),
      'POST /api/workspaces/ws1/git/switch': () => ({ status: 204 }),
      'POST /api/workspaces/ws1/git/discard': () => ({ status: 204 }),
    })
    await userEvent.click(await screen.findByRole('button', { name: /main/ }))
    expect(await screen.findByText('origin/hotfix')).toBeInTheDocument()
    expect(screen.queryByText('origin/main')).toBeNull() // already a local branch
    await userEvent.type(screen.getByLabelText('New branch'), 'feature/x')
    await userEvent.click(screen.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(calls.find((c) => c.url.endsWith('/git/switch'))?.body).toEqual({ repo: 'app', branch: 'feature/x', create: true }))

    await userEvent.click(await screen.findByRole('button', { name: 'Discard edited.go' }))
    expect(calls.some((c) => c.url.endsWith('/git/discard'))).toBe(false)
    await userEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Discard changes' }))
    await waitFor(() => expect(calls.find((c) => c.url.endsWith('/git/discard'))?.body).toEqual({ repo: 'app', paths: ['edited.go'] }))
  })

  it('offers to clone when the workspace has no repositories', async () => {
    const calls = mockApi({
      'GET /api/workspaces/ws1/git/repos': () => ({ repos: [] }),
      'POST /api/workspaces/ws1/git/clone': () => ({ status: 201, body: { repo: 'app' } }),
    })
    renderPage(<GitPanel workspaceId="ws1" onOpenFile={() => {}} />)
    await userEvent.click(await screen.findByRole('button', { name: 'Clone a repository' }))
    await userEvent.type(screen.getByLabelText('Repository URL'), 'https://github.com/acme/app.git')
    await userEvent.click(screen.getByRole('button', { name: 'Clone' }))
    await waitFor(() => expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ url: 'https://github.com/acme/app.git', dir: '', branch: '' }))
  })
})

describe('GitCredentialsTab', () => {
  const cred = { id: 'gc1', label: 'work', provider: 'github', host: 'github.com', username: 'x-access-token', createdAt: '', updatedAt: '' }

  it('lists credentials without any token and adds a write-only one', async () => {
    const calls = mockApi({
      'GET /api/workspaces/ws1/git/credentials': () => ({ credentials: [cred] }),
      'POST /api/workspaces/ws1/git/credentials': () => ({ status: 201, body: cred }),
    })
    renderPage(<GitCredentialsTab workspaceId="ws1" />)
    expect(await screen.findByText('work')).toBeInTheDocument()
    expect(screen.getByText(/github.com · x-access-token · token ••••••••/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Add credential' }))
    const token = screen.getByLabelText('Access token')
    expect(token).toHaveAttribute('type', 'password')
    expect(screen.getByRole('button', { name: 'Save credential' })).toBeDisabled()
    await userEvent.type(token, 'ghp_secrettoken123')
    await userEvent.click(screen.getByRole('button', { name: 'Save credential' }))
    await waitFor(() => expect(calls.find((c) => c.method === 'POST')?.body).toMatchObject({ provider: 'github', token: 'ghp_secrettoken123' }))
  })

  it('requires host and username for a custom host and confirms revocation', async () => {
    const calls = mockApi({
      'GET /api/workspaces/ws1/git/credentials': () => ({ credentials: [cred] }),
      'DELETE /api/workspaces/ws1/git/credentials/': () => ({ status: 204 }),
    })
    renderPage(<GitCredentialsTab workspaceId="ws1" />)
    await userEvent.click(await screen.findByRole('button', { name: 'Add credential' }))
    await userEvent.selectOptions(screen.getByLabelText('Provider'), 'custom')
    await userEvent.type(screen.getByLabelText('Access token'), 'tok-0123456789')
    expect(screen.getByRole('button', { name: 'Save credential' })).toBeDisabled()
    await userEvent.type(screen.getByLabelText('Host'), 'git.example.com')
    await userEvent.type(screen.getByLabelText('Username'), 'bot')
    expect(screen.getByRole('button', { name: 'Save credential' })).toBeEnabled()
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))

    await userEvent.click(screen.getByRole('button', { name: 'Revoke work' }))
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false)
    await userEvent.click(screen.getByRole('button', { name: 'Revoke and delete token' }))
    await waitFor(() => expect(calls.find((c) => c.method === 'DELETE')?.url).toBe('/api/workspaces/ws1/git/credentials/gc1'))
  })
})
