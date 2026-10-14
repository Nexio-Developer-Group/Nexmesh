export type ConnState = 'connecting' | 'open' | 'reconnecting' | 'offline' | 'ended' | 'failed'

export interface ConnOptions {
  url: string
  onOutput: (bytes: Uint8Array) => void
  /** Fired at the start of every (re)connection, before the replayed scrollback arrives. */
  onReset: () => void
  onExit: (code: number) => void
  onState: (s: ConnState) => void
  /** Injected for tests. */
  createSocket?: (url: string) => WebSocket
  maxAttempts?: number
}

const b64ToBytes = (s: string) => Uint8Array.from(atob(s), (c) => c.charCodeAt(0))

/**
 * One terminal session's WebSocket with automatic reconnection. The control
 * plane replays the session's scrollback on every attach, so each reconnect
 * clears the screen first (onReset) and then repaints from the replay.
 */
export class TerminalConnection {
  private ws: WebSocket | null = null
  private attempts = 0
  private timer: ReturnType<typeof setTimeout> | null = null
  private closed = false
  private exited = false

  constructor(private o: ConnOptions) { this.connect() }

  private connect() {
    this.o.onState(this.attempts === 0 ? 'connecting' : 'reconnecting')
    const ws = (this.o.createSocket ?? ((u) => new WebSocket(u)))(this.o.url)
    this.ws = ws
    ws.onopen = () => {
      this.attempts = 0
      this.o.onReset()
      this.o.onState('open')
    }
    ws.onmessage = (ev) => {
      let f: { t: string; data?: string; code?: number }
      try { f = JSON.parse(String(ev.data)) } catch { return }
      if (f.t === 'out' && f.data) this.o.onOutput(b64ToBytes(f.data))
      else if (f.t === 'exit') { this.exited = true; this.o.onExit(f.code ?? 0) }
      else if (f.t === 'offline') this.o.onState('offline')
    }
    ws.onclose = (ev) => {
      if (this.closed) return
      // 1008: the server refused the attach (session no longer exists).
      if (ev.code === 1008) { this.o.onState('ended'); return }
      if (this.exited) { this.o.onState('ended'); return }
      const max = this.o.maxAttempts ?? 8
      if (++this.attempts > max) { this.o.onState('failed'); return }
      this.o.onState('reconnecting')
      this.timer = setTimeout(() => this.connect(), Math.min(500 * 2 ** (this.attempts - 1), 10_000))
    }
  }

  /** Manual retry after 'failed'. */
  retry() {
    if (this.closed) return
    this.attempts = 0
    this.connect()
  }

  private send(f: object) {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(f))
  }
  input(data: string) { this.send({ t: 'in', data }) }
  resize(cols: number, rows: number) { this.send({ t: 'resize', cols, rows }) }

  close() {
    this.closed = true
    if (this.timer) clearTimeout(this.timer)
    this.ws?.close()
  }
}

export const terminalUrl = (ws: string, tid: string) =>
  `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/api/workspaces/${ws}/terminals/${tid}/ws`
