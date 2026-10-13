import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { AdvancedSetup, type AdvancedState } from './AdvancedSetup'
import { emptyForm, fromConfig, isPrivateAddress, isStarted, toInput } from './form'
import { CodeAgentTab } from '@/features/settings/CodeAgentTab'
import { mockApi, renderPage } from '@/test/utils'
import type { AgentConfig, AgentProvider, AgentTestResult } from '@/api/types'

const providers: AgentProvider[] = [
  { id: 'anthropic', name: 'Anthropic', tagline: 'Hosted', order: 1, hosted: true, protocols: ['anthropic'], baseUrl: 'https://api.anthropic.com', defaultModel: 'claude-sonnet-4-5', notice: 'Prompts and project context will be sent to Anthropic.', fields: [] },
  { id: 'custom', name: 'Custom or local', tagline: 'Your endpoint', order: 4, hosted: false, protocols: ['openai', 'anthropic', 'ollama'], fields: [] },
]
const ok: AgentTestResult = {
  passed: true, at: '2026-10-13T12:00:00Z', latencyMs: 640, models: [{ id: 'claude-sonnet-4-5' }, { id: 'claude-haiku-4-5' }],
  stages: ['input_validation', 'network', 'authentication', 'model', 'request'].map((stage) => ({ stage, status: 'passed' as const, message: 'ok' })),
}
const bad: AgentTestResult = {
  passed: false, failedStage: 'authentication', message: 'The key was rejected (invalid or revoked).', at: '2026-10-13T12:00:00Z',
  stages: [{ stage: 'input_validation', status: 'passed' }, { stage: 'network', status: 'passed' }, { stage: 'authentication', status: 'failed', message: 'The key was rejected (invalid or revoked).' },
    { stage: 'model', status: 'skipped' }, { stage: 'request', status: 'skipped' }],
}
const cfg = (o: Partial<AgentConfig> = {}): AgentConfig => ({
  id: 'a1', name: 'Claude for backend work', provider: 'anthropic', modelId: 'claude-sonnet-4-5', params: {}, hasKey: true, isDefault: true, createdAt: '2026-10-13T10:00:00Z',
  lastTest: { passed: true, at: '2026-10-13T10:00:00Z', stages: [] }, ...o,
})

afterEach(() => vi.unstubAllGlobals())

describe('agent form helpers', () => {
  it('flags loopback and private addresses', () => {
    for (const u of ['http://localhost:11434', 'http://127.0.0.1:8000/v1', 'http://10.0.4.12:11434/v1', 'http://192.168.1.5', 'http://172.20.0.2', 'http://[::1]:1234', 'http://169.254.169.254']) expect(isPrivateAddress(u), u).toBeTruthy()
    for (const u of ['https://api.openai.com/v1', 'http://172.32.0.1', 'http://8.8.8.8', 'not a url']) expect(isPrivateAddress(u), u).toBeNull()
  })
  it('toInput omits empty fields and keeps secrets out of names', () => {
    const f = { ...emptyForm(), provider: 'anthropic', modelId: ' claude-sonnet-4-5 ', apiKey: 'sk-1', maxOutputTokens: 2048 as const }
    const input = toInput(f, providers)
    expect(input).toMatchObject({ provider: 'anthropic', modelId: 'claude-sonnet-4-5', apiKey: 'sk-1', maxOutputTokens: 2048 })
    expect(input.endpoint).toBeUndefined()
    expect(input.protocol).toBeUndefined()
    expect(input.name).toBe('Anthropic · claude-sonnet-4-5')
    expect(input.name).not.toContain('sk-1')
  })
  it('custom models carry protocol and capabilities', () => {
    const input = toInput({ ...emptyForm(), provider: 'custom', protocol: 'ollama', endpoint: 'http://h:11434', modelId: 'qwen', name: 'Local' }, providers)
    expect(input).toMatchObject({ protocol: 'ollama', endpoint: 'http://h:11434', capabilities: { streaming: true, tools: false, vision: false } })
  })
  it('isStarted is false for an untouched section', () => {
    expect(isStarted(emptyForm())).toBe(false)
    expect(isStarted({ ...emptyForm(), apiKey: 'x' })).toBe(true)
  })
  it('fromConfig restores settings but never a key', () => {
    const f = fromConfig(cfg({ params: { maxOutputTokens: 2048, temperature: 0.2, customHeaderNames: ['X-Tenant'] } }))
    expect(f.apiKey).toBe('')
    expect(f.maxOutputTokens).toBe(2048)
    expect(f.customHeaders).toEqual([{ name: 'X-Tenant', value: '' }])
  })
})

function Harness({ onState }: { onState: (s: AdvancedState) => void }) {
  const [s, setS] = useState<AdvancedState | null>(null)
  return <><AdvancedSetup onChange={(x) => { setS(x); onState(x) }} /><output data-testid="state">{JSON.stringify({ started: s?.started, tested: s?.tested })}</output></>
}

describe('AdvancedSetup (Create Code Space)', () => {
  const routes = { 'GET /api/agent/providers': () => ({ providers }) }

  it('is collapsed and optional, and an untouched section is not "started"', async () => {
    mockApi(routes)
    let last: AdvancedState | undefined
    renderPage(<Harness onState={(s) => (last = s)} />)
    expect(screen.getByText('Optional')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Expand' })).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByText('Code Agent')).not.toBeInTheDocument()
    await waitFor(() => expect(last).toBeDefined())
    expect(last).toMatchObject({ started: false, tested: false, input: null })
  })

  it('a started but untested section reports started and blocks until Test Configuration passes', async () => {
    const calls = mockApi({ ...routes, 'POST /api/agent/test': () => ok })
    let last!: AdvancedState
    renderPage(<Harness onState={(s) => (last = s)} />)
    await userEvent.click(screen.getByRole('button', { name: 'Expand' }))
    await userEvent.click(await screen.findByRole('radio', { name: /Anthropic/ }))
    await userEvent.type(screen.getByLabelText('API key'), 'sk-ant-secret-0123')
    await waitFor(() => expect(last.started).toBe(true))
    expect(last).toMatchObject({ tested: false, input: null })
    expect(screen.getByText(/You started configuring a code agent/)).toBeInTheDocument()
    expect(screen.getByLabelText('Model')).toHaveValue('claude-sonnet-4-5') // provider default
    await userEvent.click(screen.getByRole('button', { name: 'Test Configuration' }))
    await waitFor(() => expect(last.tested).toBe(true))
    expect(last.input).toMatchObject({ provider: 'anthropic', modelId: 'claude-sonnet-4-5', apiKey: 'sk-ant-secret-0123' })
    expect(screen.getAllByRole('status').some((e) => /Passed/.test(e.textContent ?? ''))).toBe(true)
    expect(calls.find((c) => c.url.endsWith('/api/agent/test'))).toBeTruthy()
    // Editing after the pass invalidates it.
    await userEvent.type(screen.getByLabelText('API key'), 'x')
    await waitFor(() => expect(last.tested).toBe(false))
    // Clearing the section makes it optional again.
    await userEvent.click(screen.getByRole('button', { name: 'Clear section' }))
    await waitFor(() => expect(last.started).toBe(false))
  })

  it('shows a specific failure and keeps the section blocking', async () => {
    mockApi({ ...routes, 'POST /api/agent/test': () => bad })
    let last!: AdvancedState
    renderPage(<Harness onState={(s) => (last = s)} />)
    await userEvent.click(screen.getByRole('button', { name: 'Expand' }))
    await userEvent.click(await screen.findByRole('radio', { name: /Anthropic/ }))
    await userEvent.type(screen.getByLabelText('API key'), 'bad-key-bad-key')
    await userEvent.click(screen.getByRole('button', { name: 'Test Configuration' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Key accepted failed. The key was rejected')
    expect(last.tested).toBe(false)
    expect(screen.getByLabelText('API key')).toHaveValue('bad-key-bad-key')
  })

  it('custom endpoints warn about loopback and private addresses', async () => {
    mockApi(routes)
    renderPage(<Harness onState={() => {}} />)
    await userEvent.click(screen.getByRole('button', { name: 'Expand' }))
    await userEvent.click(await screen.findByRole('radio', { name: /Custom or local/ }))
    await userEvent.type(screen.getByLabelText('Endpoint base URL'), 'http://10.0.4.12:11434/v1')
    expect(screen.getByText(/Private address\. It is reachable only if/)).toBeInTheDocument()
    expect(screen.getByLabelText(/API key or token/)).toBeInTheDocument() // optional for local servers
    expect(screen.getByRole('switch', { name: /Tool calling/ })).toHaveAttribute('aria-checked', 'false')
  })
})

describe('CodeAgentTab', () => {
  const routes = () => ({
    'GET /api/agent/providers': () => ({ providers }),
    'GET /api/workspaces/w1/agent-configs': () => ({ configs: [cfg(), cfg({ id: 'a2', name: 'GPT for frontend review', provider: 'anthropic', modelId: 'gpt-4.1', isDefault: false, lastTest: { passed: false, at: '2026-10-12T10:00:00Z', stages: [] } })] }),
  })

  it('lists configurations with status, default marker and actions, never a key', async () => {
    mockApi(routes())
    renderPage(<CodeAgentTab workspaceId="w1" />)
    expect(await screen.findByText('Claude for backend work')).toBeInTheDocument()
    expect(screen.getByText('Default')).toBeInTheDocument()
    expect(screen.getByText(/Test failed/)).toBeInTheDocument()
    expect(screen.getByText(/Passed/)).toBeInTheDocument()
    expect(screen.getAllByRole('button', { name: 'Replace API key' })).toHaveLength(2)
    expect(screen.getAllByRole('button', { name: 'Set as default' })).toHaveLength(1)
    expect(screen.getByText(/Prompts and project context are sent to the selected provider/)).toBeInTheDocument()
  })

  it('Save is disabled until a passing test and any edit disables it again', async () => {
    const calls = mockApi({ ...routes(), 'POST /api/agent/test': () => ok, 'POST /api/workspaces/w1/agent-configs': () => ({ status: 201, body: cfg({ id: 'a3' }) }) })
    renderPage(<CodeAgentTab workspaceId="w1" />)
    await userEvent.click(await screen.findByRole('button', { name: 'Add configuration' }))
    await userEvent.click(screen.getByRole('radio', { name: /Anthropic/ }))
    await userEvent.type(screen.getByLabelText('Configuration name'), 'Claude two')
    await userEvent.type(screen.getByLabelText('API key'), 'sk-ant-xxxxxxxx')
    const save = screen.getByRole('button', { name: 'Save' })
    expect(save).toBeDisabled()
    await userEvent.click(screen.getByRole('button', { name: 'Test Configuration' }))
    await waitFor(() => expect(save).toBeEnabled())
    await userEvent.type(screen.getByLabelText('Configuration name'), '!')
    expect(save).toBeDisabled()
    await userEvent.click(screen.getByRole('button', { name: 'Test Configuration' }))
    await waitFor(() => expect(save).toBeEnabled())
    await userEvent.click(save)
    await waitFor(() => expect(calls.some((c) => c.method === 'POST' && c.url.endsWith('/agent-configs'))).toBe(true))
    const body = calls.find((c) => c.method === 'POST' && c.url.endsWith('/agent-configs'))!.body as Record<string, unknown>
    expect(body).toMatchObject({ name: 'Claude two!', provider: 'anthropic', apiKey: 'sk-ant-xxxxxxxx' })
  })

  it('editing a saved configuration keeps the stored key without retyping it', async () => {
    const calls = mockApi({ ...routes(), 'POST /api/workspaces/w1/agent-configs/a1/test': () => ok, 'PUT /api/workspaces/w1/agent-configs/a1': () => cfg() })
    renderPage(<CodeAgentTab workspaceId="w1" />)
    await userEvent.click((await screen.findAllByRole('button', { name: 'Edit' }))[0])
    const key = screen.getByLabelText('API key')
    expect(key).toHaveValue('')
    expect(key).toHaveAttribute('placeholder', expect.stringContaining('stored'))
    await userEvent.click(screen.getByRole('button', { name: 'Test Configuration' }))
    await waitFor(() => expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled())
    const body = calls.find((c) => c.url.endsWith('/a1/test'))!.body as Record<string, unknown>
    expect(body.apiKey).toBeUndefined()
  })

  it('Delete explains that the provider key is not revoked', async () => {
    const calls = mockApi({ ...routes(), 'DELETE /api/workspaces/w1/agent-configs/a2': () => ({ status: 204 }) })
    renderPage(<CodeAgentTab workspaceId="w1" />)
    await userEvent.click((await screen.findAllByRole('button', { name: 'Delete' }))[1])
    expect(screen.getByRole('dialog')).toHaveTextContent('does not revoke the key at the provider')
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false)
    await userEvent.click(screen.getByRole('button', { name: 'Delete configuration' }))
    await waitFor(() => expect(calls.some((c) => c.method === 'DELETE')).toBe(true))
  })

  it('a failed key replacement shows why and does not claim success', async () => {
    mockApi({ ...routes(), 'PUT /api/workspaces/w1/agent-configs/a1/key': () => ({ status: 422, body: { error: { code: 'test_failed', message: 'The key was rejected (invalid or revoked).' } } }) })
    renderPage(<CodeAgentTab workspaceId="w1" />)
    await userEvent.click((await screen.findAllByRole('button', { name: 'Replace API key' }))[0])
    await userEvent.type(screen.getByLabelText('New API key'), 'nope-nope-nope')
    await act(async () => { await userEvent.click(screen.getByRole('button', { name: 'Test and replace' })) })
    expect(await screen.findByRole('alert')).toHaveTextContent('The key was rejected')
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })
})
