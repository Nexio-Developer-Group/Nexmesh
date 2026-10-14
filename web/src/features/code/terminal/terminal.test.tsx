import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { TerminalConnection, type ConnState } from './connection'
import { TerminalPanel } from './TerminalPanel'
import { mockApi, renderPage } from '@/test/utils'

vi.mock('./TerminalView', () => ({
  TerminalView: ({ sessionId, visible }: { sessionId: string; visible: boolean }) => <div data-testid={`term-${sessionId}`} data-visible={visible} />,
}))

class FakeSocket {
  static instances: FakeSocket[] = []
  static OPEN = 1
  readyState = 0
  sent: string[] = []
  onopen: (() => void) | null = null
  onmessage: ((e: { data: string }) => void) | null = null
  onclose: ((e: { code: number }) => void) | null = null
  constructor(public url: string) { FakeSocket.instances.push(this) }
  send(d: string) { this.sent.push(d) }
  close() { this.readyState = 3 }
  open() { this.readyState = 1; this.onopen?.() }
  msg(f: object) { this.onmessage?.({ data: JSON.stringify(f) }) }
  drop(code = 1006) { this.readyState = 3; this.onclose?.({ code }) }
}

function conn(extra: Partial<ConstructorParameters<typeof TerminalConnection>[0]> = {}) {
  FakeSocket.instances = []
  vi.stubGlobal('WebSocket', FakeSocket)
  const states: ConnState[] = []
  const out: string[] = []
  const resets = { n: 0 }
  const exits: number[] = []
  const c = new TerminalConnection({
    url: 'ws://x/t', onState: (s) => states.push(s), onOutput: (b) => out.push(new TextDecoder().decode(b)), onReset: () => { resets.n++ }, onExit: (c) => exits.push(c),
    createSocket: (u) => new FakeSocket(u) as unknown as WebSocket, ...extra,
  })
  return { c, states, out, resets, exits, sock: () => FakeSocket.instances[FakeSocket.instances.length - 1] }
}

describe('TerminalConnection', () => {
  beforeEach(() => { vi.useFakeTimers() })
  afterEach(() => { vi.useRealTimers() })

  it('decodes output, sends input/resize and resets on each open', () => {
    const t = conn()
    t.sock().open()
    expect(t.states).toEqual(['connecting', 'open'])
    expect(t.resets.n).toBe(1)
    t.sock().msg({ t: 'out', data: btoa(String.fromCharCode(...new TextEncoder().encode('hello ✓'))) })
    expect(t.out).toEqual(['hello ✓'])
    t.c.input('ls\r')
    t.c.resize(100, 30)
    expect(t.sock().sent.map((s) => JSON.parse(s))).toEqual([{ t: 'in', data: 'ls\r' }, { t: 'resize', cols: 100, rows: 30 }])
  })

  it('reconnects with backoff and repaints from the replay', () => {
    const t = conn()
    t.sock().open()
    t.sock().drop()
    expect(t.states.at(-1)).toBe('reconnecting')
    expect(FakeSocket.instances).toHaveLength(1)
    vi.advanceTimersByTime(499); expect(FakeSocket.instances).toHaveLength(1)
    vi.advanceTimersByTime(1); expect(FakeSocket.instances).toHaveLength(2)
    t.sock().drop() // second failure waits longer
    vi.advanceTimersByTime(999); expect(FakeSocket.instances).toHaveLength(2)
    vi.advanceTimersByTime(1); expect(FakeSocket.instances).toHaveLength(3)
    t.sock().open()
    expect(t.resets.n).toBe(2) // screen cleared before the scrollback replay
    expect(t.states.at(-1)).toBe('open')
  })

  it('does not retry when the session is gone, after exit, or after close()', () => {
    const gone = conn()
    gone.sock().open(); gone.sock().drop(1008)
    expect(gone.states.at(-1)).toBe('ended')
    vi.advanceTimersByTime(60_000); expect(FakeSocket.instances).toHaveLength(1)

    const done = conn()
    done.sock().open(); done.sock().msg({ t: 'exit', code: 3 }); done.sock().drop(1000)
    expect(done.exits).toEqual([3]); expect(done.states.at(-1)).toBe('ended')

    const closed = conn()
    closed.sock().open(); closed.c.close(); closed.sock().drop()
    vi.advanceTimersByTime(60_000); expect(FakeSocket.instances).toHaveLength(1)
  })

  it('gives up after the attempt budget and can be retried manually', () => {
    const t = conn({ maxAttempts: 2 })
    t.sock().drop(); vi.advanceTimersByTime(500)
    t.sock().drop(); vi.advanceTimersByTime(1000)
    t.sock().drop()
    expect(t.states.at(-1)).toBe('failed')
    t.c.retry()
    expect(FakeSocket.instances).toHaveLength(4)
    t.sock().open()
    expect(t.states.at(-1)).toBe('open')
  })

  it('reports an agent-offline notice from the server', () => {
    const t = conn()
    t.sock().open(); t.sock().msg({ t: 'offline' })
    expect(t.states.at(-1)).toBe('offline')
  })
})

describe('TerminalPanel', () => {
  const info = (id: string, title: string, exited = false) => ({ id, title, exited, exitCode: 0, cols: 80, rows: 24 })

  it('starts a terminal when none exist and adds more from the + button', async () => {
    let n = 0
    const sessions: ReturnType<typeof info>[] = []
    const calls = mockApi({
      'GET /api/workspaces/ws1/terminals': () => ({ sessions }),
      'POST /api/workspaces/ws1/terminals': () => { n++; const s = info('t' + n, 'Terminal ' + n); sessions.push(s); return { status: 201, body: s } },
    })
    renderPage(<TerminalPanel workspaceId="ws1" />)
    expect(await screen.findByTestId('term-t1')).toHaveAttribute('data-visible', 'true')
    await userEvent.click(screen.getByRole('button', { name: 'New terminal' }))
    expect(await screen.findByTestId('term-t2')).toHaveAttribute('data-visible', 'true')
    expect(screen.getByTestId('term-t1')).toHaveAttribute('data-visible', 'false')
    expect(calls.filter((c) => c.method === 'POST')).toHaveLength(2)
  })

  it('reattaches to a session that outlived the browser instead of starting a new one', async () => {
    const calls = mockApi({ 'GET /api/workspaces/ws1/terminals': () => ({ sessions: [info('t7', 'Terminal 7')] }) })
    renderPage(<TerminalPanel workspaceId="ws1" />)
    expect(await screen.findByTestId('term-t7')).toHaveAttribute('data-visible', 'true')
    expect(calls.some((c) => c.method === 'POST')).toBe(false)
  })

  it('closes a session on the server and switches tabs', async () => {
    const sessions = [info('t1', 'Terminal 1'), info('t2', 'Terminal 2')]
    const calls = mockApi({
      'GET /api/workspaces/ws1/terminals': () => ({ sessions: [...sessions] }),
      'DELETE /api/workspaces/ws1/terminals/': (u) => { const id = u.split('/').pop(); sessions.splice(sessions.findIndex((s) => s.id === id), 1); return { status: 204 } },
    })
    renderPage(<TerminalPanel workspaceId="ws1" />)
    await screen.findByTestId('term-t1')
    await userEvent.click(screen.getByRole('button', { name: 'Close Terminal 1' }))
    await waitFor(() => expect(screen.queryByTestId('term-t1')).toBeNull())
    expect(calls.find((c) => c.method === 'DELETE')?.url).toBe('/api/workspaces/ws1/terminals/t1')
    expect(screen.getByTestId('term-t2')).toHaveAttribute('data-visible', 'true')
  })

  it('collapses and expands', async () => {
    mockApi({ 'GET /api/workspaces/ws1/terminals': () => ({ sessions: [info('t1', 'Terminal 1')] }) })
    renderPage(<TerminalPanel workspaceId="ws1" />)
    await screen.findByTestId('term-t1')
    await userEvent.click(screen.getByRole('button', { name: 'Collapse terminal panel' }))
    expect(screen.getByTestId('term-t1')).toHaveAttribute('data-visible', 'false')
    await userEvent.click(screen.getByRole('button', { name: 'Expand terminal panel' }))
    expect(screen.getByTestId('term-t1')).toHaveAttribute('data-visible', 'true')
  })
})
