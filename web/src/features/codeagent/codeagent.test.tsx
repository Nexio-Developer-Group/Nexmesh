import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AgentPanel } from './AgentPanel'
import { AssistDialog } from './AssistDialog'
import { GatesTab } from './GatesTab'
import { ReviewDialog } from './ReviewDialog'
import { RulesTab } from './RulesTab'
import { mockApi, renderPage } from '@/test/utils'
import type { AgentSession, ChangeSet, Hunk } from './hooks'

const hunk = (id: number, line: string): Hunk => ({ id, oldStart: id * 20 + 1, oldLines: 3, newStart: id * 20 + 1, newLines: 3, added: 1, removed: 1,
  lines: [{ kind: 'context', text: 'keep' }, { kind: 'del', text: `old ${line}` }, { kind: 'add', text: `new ${line}` }] })

const set = (extra: Partial<ChangeSet> = {}): ChangeSet => ({
  id: 'cs1', title: 'Greet by name', summary: 'Use the name argument', actor: 'agent', status: 'proposed',
  files: [{ path: 'app.py', op: 'modify', status: 'pending', added: 2, removed: 2, hunks: [hunk(0, 'early'), hunk(1, 'late')] }, { path: 'util.py', op: 'create', status: 'pending', added: 1, removed: 0, hunks: [hunk(0, 'util')] }],
  rules: [{ id: '.nexmesh/rules/style.md', title: 'Style', scope: 'global', hash: 'ab' }],
  impact: { components: [{ id: 'c1', name: 'Redis' }], variables: ['REDIS_URL'] }, createdAt: '2026-01-01T00:00:00Z', ...extra,
})

describe('ReviewDialog', () => {
  it('shows diffs by file with rules and impact, and accepts only the chosen hunks', async () => {
    const calls = mockApi({ 'GET /api/workspaces/ws/changes/cs1': () => set(), 'GET /api/workspaces/ws/gates/runs': () => ({ runs: [] }), 'POST /api/workspaces/ws/changes/cs1/accept': () => set({ status: 'partially_applied' }) })
    renderPage(<ReviewDialog workspaceId="ws" setId="cs1" onClose={() => {}} />)
    const dialog = await screen.findByRole('dialog', { name: 'Greet by name' })
    expect(within(dialog).getByText('Proposed by the Code Agent')).toBeInTheDocument()
    expect(within(dialog).getByText('Style')).toBeInTheDocument()
    expect(within(dialog).getByText(/REDIS_URL/)).toBeInTheDocument()
    expect(within(dialog).getByText('new early')).toBeInTheDocument()
    expect(within(dialog).getByText('old early')).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Accept all' })).toBeEnabled()
    // Untick the second hunk of app.py and all of util.py.
    await userEvent.click(within(dialog).getByRole('checkbox', { name: 'Accept change 2 in app.py' }))
    await userEvent.click(within(dialog).getByRole('checkbox', { name: 'Accept util.py' }))
    await userEvent.click(within(dialog).getByRole('button', { name: 'Accept 1 of 3 changes' }))
    await waitFor(() => expect(calls.find((c) => c.method === 'POST')?.body).toEqual({ files: [{ path: 'app.py', hunks: [0] }] }))
  })

  it('accept all sends all, reject sends nothing to apply', async () => {
    const calls = mockApi({ 'GET /api/workspaces/ws/changes/cs1': () => set(), 'GET /api/workspaces/ws/gates/runs': () => ({ runs: [] }), 'POST /api/workspaces/ws/changes/cs1/accept': () => set({ status: 'applied' }), 'POST /api/workspaces/ws/changes/cs1/reject': () => set({ status: 'rejected' }) })
    const onClose = vi.fn()
    renderPage(<ReviewDialog workspaceId="ws" setId="cs1" onClose={onClose} />)
    await userEvent.click(await screen.findByRole('button', { name: 'Accept all' }))
    await waitFor(() => expect(calls.find((c) => c.url.endsWith('/accept'))?.body).toEqual({ all: true }))
    expect(onClose).toHaveBeenCalled()
  })

  it('a required failing gate blocks accept until the user overrides with a reason', async () => {
    const gate = { configured: true, ran: true, passed: false, failing: ['Tests'], runId: 'gr1' }
    const calls = mockApi({
      'GET /api/workspaces/ws/changes/cs1': () => set({ gate }),
      'GET /api/workspaces/ws/gates/runs/gr1': () => ({ id: 'gr1', status: 'done', passed: false, requiredFailed: ['Tests'], startedAt: '', results: [{ gateId: 't', name: 'Tests', kind: 'test', policy: 'required', status: 'failed', exitCode: 1, millis: 12, log: '$ go test\nFAIL main_test.go' }] }),
      'GET /api/workspaces/ws/gates/runs': () => ({ runs: [{ id: 'gr1', status: 'done', passed: false, requiredFailed: ['Tests'], startedAt: '', results: [] }] }),
      'POST /api/workspaces/ws/changes/cs1/accept': () => set({ status: 'applied' }),
    })
    renderPage(<ReviewDialog workspaceId="ws" setId="cs1" onClose={() => {}} />)
    const dialog = await screen.findByRole('dialog', { name: 'Greet by name' })
    expect(await within(dialog).findByText(/Required gates failed: Tests/)).toBeInTheDocument()
    expect(within(dialog).getByRole('button', { name: 'Accept all' })).toBeDisabled()
    expect(await within(dialog).findByLabelText('Log for Tests')).toHaveTextContent('FAIL main_test.go')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Override the gates…' }))
    expect(within(dialog).getByRole('button', { name: 'Accept all' })).toBeDisabled() // a reason is required
    await userEvent.type(within(dialog).getByLabelText(/Reason for accepting despite the gates/), 'flaky, tracked in the backlog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Accept all' }))
    await waitFor(() => expect(calls.find((c) => c.url.endsWith('/accept'))?.body).toEqual({ all: true, overrideGates: 'flaky, tracked in the backlog' }))
  })

  it('a decided set is read only and warnings are announced', async () => {
    mockApi({ 'GET /api/workspaces/ws/changes/cs1': () => set({ status: 'applied', warnings: ['infra/main.tf is a protected path (Infra)'] }), 'GET /api/workspaces/ws/gates/runs': () => ({ runs: [] }) })
    renderPage(<ReviewDialog workspaceId="ws" setId="cs1" onClose={() => {}} />)
    const dialog = await screen.findByRole('dialog', { name: 'Greet by name' })
    expect(within(dialog).getByRole('alert', { name: 'Warnings' })).toHaveTextContent('protected path')
    expect(within(dialog).queryByRole('button', { name: /Accept/ })).toBeNull()
    expect(within(dialog).queryByRole('checkbox')).toBeNull()
  })
})

const session = (extra: Partial<AgentSession> = {}): AgentSession => ({ id: 's1', title: 'Fix greet', status: 'idle', turns: [], inputTokens: 0, outputTokens: 0, updatedAt: '', ...extra })

describe('AgentPanel', () => {
  const base = () => ({
    'GET /api/workspaces/ws/agent/sessions/s1': () => session({
      turns: [
        { id: 't1', role: 'user', content: 'rename greet', at: '' },
        { id: 't2', role: 'assistant', content: 'I looked at it.', at: '', calls: [
          { id: 'c1', name: 'read_file', args: { path: 'app.py' }, status: 'ok' },
          { id: 'c2', name: 'read_file', args: { path: '.env' }, status: 'denied', result: 'Not allowed: that file can hold secrets' },
          { id: 'c3', name: 'propose_changes', args: { title: 'Greet by name' }, status: 'ok', changeSetId: 'cs1' },
          { id: 'c4', name: 'propose_card', args: { catalog_id: 'redis' }, status: 'ok', proposalId: 'p1' },
        ] },
      ],
    }),
    'GET /api/workspaces/ws/agent/sessions': () => ({ sessions: [{ id: 's1', title: 'Fix greet', status: 'idle', updatedAt: '', turns: 2 }] }),
    'GET /api/workspaces/ws/agent/proposals': () => ({ proposals: [{ id: 'p1', kind: 'card', reason: 'cache sessions', status: 'pending', catalogId: 'redis', cardName: 'Redis' }] }),
    'GET /api/workspaces/ws/changes': () => ({ changeSets: [set()] }),
  })

  it('shows the conversation with tool activity, denials and review links', async () => {
    mockApi({ 'GET /api/workspaces/ws/changes/cs1': () => set(), 'GET /api/workspaces/ws/gates/runs': () => ({ runs: [] }), ...base() })
    renderPage(<AgentPanel workspaceId="ws" onClose={() => {}} onHighlight={() => {}} />)
    const log = await screen.findByRole('log', { name: 'Conversation' })
    expect(await within(log).findByText('rename greet')).toBeInTheDocument()
    expect(within(log).getByText('Read app.py')).toBeInTheDocument()
    expect(within(log).getByText('Not allowed')).toBeInTheDocument()
    expect(within(log).getByText(/that file can hold secrets/)).toBeInTheDocument()
    await userEvent.click(within(log).getByRole('button', { name: 'Review changes' }))
    expect(await screen.findByRole('dialog', { name: 'Greet by name' })).toBeInTheDocument()
  })

  it('canvas proposals are accepted or dismissed by the user', async () => {
    const calls = mockApi({ ...base(), 'POST /api/workspaces/ws/agent/proposals/p1/accept': () => ({ id: 'p1', status: 'accepted' }) })
    renderPage(<AgentPanel workspaceId="ws" onClose={() => {}} onHighlight={() => {}} />)
    await userEvent.click(await screen.findByRole('button', { name: 'Add as draft card' }))
    await waitFor(() => expect(calls.some((c) => c.method === 'POST' && c.url.endsWith('/proposals/p1/accept'))).toBe(true))
  })

  it('a risky command waits for the user with the exact command shown', async () => {
    const calls = mockApi({
      ...base(),
      'GET /api/workspaces/ws/agent/sessions/s1': () => session({ status: 'awaiting_approval', pending: { callId: 'c9', command: 'rm -rf build', reason: 'It is not on the list of read-only, build and test commands.' } }),
      'POST /api/workspaces/ws/agent/sessions/s1/approve': () => undefined,
    })
    renderPage(<AgentPanel workspaceId="ws" onClose={() => {}} onHighlight={() => {}} />)
    const box = await screen.findByRole('region', { name: 'Approval needed' })
    expect(box).toHaveTextContent('rm -rf build')
    await userEvent.click(within(box).getByRole('button', { name: 'Decline' }))
    await waitFor(() => expect(calls.find((c) => c.url.endsWith('/approve'))?.body).toEqual({ callId: 'c9', approve: false }))
  })

  it('sends a message, creating a conversation first when there is none', async () => {
    const calls = mockApi({
      'GET /api/workspaces/ws/agent/sessions/s9': () => session({ id: 's9', status: 'running' }),
      'GET /api/workspaces/ws/agent/sessions': () => ({ sessions: [] }),
      'GET /api/workspaces/ws/changes': () => ({ changeSets: [] }),
      'POST /api/workspaces/ws/agent/sessions/s9/messages': () => session({ id: 's9', status: 'running', turns: [{ id: 'u', role: 'user', content: 'hello', at: '' }] }),
      'POST /api/workspaces/ws/agent/sessions': () => session({ id: 's9' }),
    })
    renderPage(<AgentPanel workspaceId="ws" onClose={() => {}} onHighlight={() => {}} />)
    await userEvent.type(await screen.findByLabelText('Message to the agent'), 'hello')
    await userEvent.click(screen.getByRole('button', { name: 'Send' }))
    await waitFor(() => expect(calls.find((c) => c.url.endsWith('/s9/messages'))?.body).toEqual({ text: 'hello' }))
  })

  it('settings change the permission level and show the exact context', async () => {
    const calls = mockApi({
      'GET /api/workspaces/ws/agent/sessions': () => ({ sessions: [] }), 'GET /api/workspaces/ws/changes': () => ({ changeSets: [] }),
      'GET /api/workspaces/ws/agent/settings': () => ({ level: 'propose', autoAccept: false }),
      'PUT /api/workspaces/ws/agent/settings': (_u: string, init?: RequestInit) => JSON.parse(String(init?.body)),
      'GET /api/workspaces/ws/agent/context': () => ({ context: {}, text: 'Workspace: w\nVariables: STRIPE_KEY (secret)\n' }),
    })
    renderPage(<AgentPanel workspaceId="ws" onClose={() => {}} onHighlight={() => {}} />)
    await userEvent.click(screen.getByRole('tab', { name: 'Settings' }))
    expect(await screen.findByRole('radio', { name: /Propose changes/ })).toBeChecked()
    await userEvent.click(screen.getByRole('radio', { name: /Propose and run/ }))
    await waitFor(() => expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({ level: 'execute', autoAccept: false }))
    await userEvent.click(screen.getByRole('button', { name: 'Show what the agent can see' }))
    expect(await screen.findByLabelText('Agent context')).toHaveTextContent('STRIPE_KEY (secret)')
  })

  it('checkpoints restore with a warning when files were edited since', async () => {
    let attempts = 0
    const calls = mockApi({
      'GET /api/workspaces/ws/agent/sessions': () => ({ sessions: [] }), 'GET /api/workspaces/ws/changes': () => ({ changeSets: [] }),
      'GET /api/workspaces/ws/checkpoints': () => ({ checkpoints: [{ id: 'cp1', label: 'Greet by name', files: ['app.py'], createdAt: '2026-01-01T00:00:00Z' }] }),
      'POST /api/workspaces/ws/checkpoints/cp1/revert': () => (++attempts === 1 ? { status: 422, body: { error: { code: 'edited_since', message: 'These files were edited after the change was applied: app.py.' } } } : { id: 'cp1' }),
    })
    renderPage(<AgentPanel workspaceId="ws" onClose={() => {}} onHighlight={() => {}} />)
    await userEvent.click(screen.getByRole('tab', { name: 'Changes' }))
    await userEvent.click(await screen.findByRole('button', { name: 'Restore checkpoint Greet by name' }))
    await userEvent.click(screen.getByRole('button', { name: 'Restore files' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('edited after the change')
    await userEvent.click(screen.getByRole('button', { name: 'Restore anyway' }))
    await userEvent.click(screen.getByRole('button', { name: 'Restore and overwrite my edits' }))
    await waitFor(() => expect(calls.filter((c) => c.url.endsWith('/revert')).map((c) => c.body)).toEqual([{ force: false }, { force: true }]))
  })
})

describe('AssistDialog', () => {
  it('previews exactly what is sent and sends only the previewed text', async () => {
    const calls = mockApi({
      'POST /api/workspaces/ws/components/c1/assist/preview': () => ({ id: 'prv1', kind: 'diagnose', cardName: 'Redis', text: 'Diagnose Redis\n## Recent logs\npassword=[hidden]', sections: [{ label: 'Card', chars: 10 }, { label: 'Recent logs', chars: 20 }], redactions: 2, expiresAt: '' }),
      'POST /api/workspaces/ws/components/c1/assist': () => session({ id: 's5' }),
    })
    const onSent = vi.fn()
    renderPage(<AssistDialog workspaceId="ws" componentId="c1" cardName="Redis" kind="diagnose" onClose={() => {}} onSent={onSent} />)
    const text = await screen.findByLabelText('Text that will be sent')
    expect(text).toHaveTextContent('password=[hidden]')
    expect(screen.getByRole('status')).toHaveTextContent('2 values hidden')
    expect(calls.some((c) => c.url.endsWith('/assist') && c.method === 'POST')).toBe(false) // nothing sent yet
    await userEvent.click(screen.getByRole('button', { name: 'Send to the agent' }))
    await waitFor(() => expect(calls.find((c) => c.url.endsWith('/assist'))?.body).toEqual({ previewId: 'prv1' }))
    await waitFor(() => expect(onSent).toHaveBeenCalledWith('s5'))
  })
})

describe('RulesTab', () => {
  it('lists rules, saves a path rule with protected paths, and deletes with confirmation', async () => {
    const calls = mockApi({
      'GET /api/workspaces/ws/rules': () => ({ directory: '.nexmesh/rules', rules: [
        { id: 'AGENTS.md', name: 'AGENTS', title: 'AGENTS.md', scope: 'global', body: 'Be kind.', hash: 'x', readOnly: true },
        { id: '.nexmesh/rules/api.md', name: 'api', title: 'API style', scope: 'path', paths: ['src/api/**'], protect: ['migrations/'], body: 'snake_case', hash: 'y' },
        { id: '.nexmesh/rules/bad.md', name: 'bad', title: 'bad', scope: 'global', body: '', hash: 'z', problem: 'The front matter is not valid YAML.' }] }),
      'PUT /api/workspaces/ws/rules': () => ({}),
      'DELETE /api/workspaces/ws/rules/api': () => undefined,
    })
    renderPage(<RulesTab workspaceId="ws" />)
    expect(await screen.findByText('API style')).toBeInTheDocument()
    expect(screen.getByText(/front matter is not valid YAML/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Edit AGENTS.md' })).toBeNull()
    await userEvent.click(screen.getByRole('button', { name: 'New rule' }))
    const dlg = await screen.findByRole('dialog', { name: 'New rule' })
    await userEvent.type(within(dlg).getByLabelText('Title'), 'Tests first')
    await userEvent.click(within(dlg).getByRole('radio', { name: 'Paths' }))
    await userEvent.type(within(dlg).getByLabelText(/Path patterns/), 'src/**')
    await userEvent.type(within(dlg).getByLabelText('The rule'), 'Write a test first.')
    await userEvent.type(within(dlg).getByLabelText(/Protected paths/), 'infra/')
    await userEvent.click(within(dlg).getByRole('button', { name: 'Save rule' }))
    await waitFor(() => expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({ title: 'Tests first', scope: 'path', components: [], paths: ['src/**'], protect: ['infra/'], body: 'Write a test first.' }))
    await userEvent.click(screen.getByRole('button', { name: 'Delete API style' }))
    await userEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Delete rule' }))
    await waitFor(() => expect(calls.some((c) => c.method === 'DELETE' && c.url.endsWith('/rules/api'))).toBe(true))
  })
})

describe('GatesTab', () => {
  it('detection only suggests; saving confirms; gates can be made required or advisory', async () => {
    const calls = mockApi({
      'GET /api/workspaces/ws/gates/detect': () => ({ gates: [{ id: 'go-test', name: 'Go tests', kind: 'test', command: 'go test ./...', policy: 'required', enabled: true, timeoutSec: 120, detected: 'go.mod found' }] }),
      'GET /api/workspaces/ws/gates/runs': () => ({ runs: [] }),
      'GET /api/workspaces/ws/gates': () => ({ gates: [], confirmed: false }),
      'PUT /api/workspaces/ws/gates': (_u: string, init?: RequestInit) => ({ ...JSON.parse(String(init?.body)), confirmed: true }),
    })
    renderPage(<GatesTab workspaceId="ws" />)
    expect(await screen.findByText(/No gates yet/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Detect project tooling' }))
    expect(await screen.findByDisplayValue('Go tests')).toBeInTheDocument()
    expect(screen.getByText('Detected: go.mod found')).toBeInTheDocument()
    expect(calls.some((c) => c.method === 'PUT')).toBe(false)
    await userEvent.selectOptions(screen.getByLabelText('Policy'), 'advisory')
    await userEvent.click(screen.getByRole('button', { name: 'Confirm and save gates' }))
    await waitFor(() => expect(calls.find((c) => c.method === 'PUT')?.body).toMatchObject({ gates: [{ id: 'go-test', policy: 'advisory', command: 'go test ./...' }] }))
    expect(await screen.findByRole('button', { name: 'Run all gates' })).toBeInTheDocument()
  })
})
