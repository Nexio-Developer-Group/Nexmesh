import { FitAddon } from '@xterm/addon-fit'
import { Terminal } from '@xterm/xterm'
import '@xterm/xterm/css/xterm.css'
import { useEffect, useRef, useState } from 'react'
import { Button } from '@/components/ui/Button'
import { TerminalConnection, terminalUrl, type ConnState } from './connection'

const theme = {
  background: '#070d1f', foreground: '#e6ecf8', cursor: '#00b8ff', selectionBackground: '#1676fe55',
  black: '#0b1530', red: '#ff6b81', green: '#3ddc97', yellow: '#ffd166', blue: '#4da3ff', magenta: '#a78bfa', cyan: '#00b8ff', white: '#e6ecf8',
}

interface Props {
  workspaceId: string
  sessionId: string
  title: string
  visible: boolean
  onEnded: () => void
}

const STATE_TEXT: Record<ConnState, string> = {
  connecting: 'Connecting…', open: '', reconnecting: 'Connection lost. Reconnecting…', offline: 'The workspace agent went offline. Reconnecting…',
  ended: 'This terminal session has ended.', failed: 'Could not reconnect.',
}

/** One xterm instance bound to a persistent PTY session. Stays mounted while hidden so output keeps flowing. */
export function TerminalView({ workspaceId, sessionId, title, visible, onEnded }: Props) {
  const host = useRef<HTMLDivElement>(null)
  const term = useRef<Terminal | null>(null)
  const fit = useRef<FitAddon | null>(null)
  const conn = useRef<TerminalConnection | null>(null)
  const [state, setState] = useState<ConnState>('connecting')
  const [exit, setExit] = useState<number | null>(null)
  const endedRef = useRef(onEnded)
  endedRef.current = onEnded

  useEffect(() => {
    const t = new Terminal({ theme, fontFamily: "'JetBrains Mono', ui-monospace, monospace", fontSize: 13, cursorBlink: true, scrollback: 5000, allowProposedApi: false })
    const f = new FitAddon()
    t.loadAddon(f)
    t.open(host.current!)
    term.current = t
    fit.current = f
    const doFit = () => { try { f.fit(); conn.current?.resize(t.cols, t.rows) } catch { /* hidden or zero-sized */ } }
    const c = new TerminalConnection({
      url: terminalUrl(workspaceId, sessionId),
      onOutput: (b) => t.write(b),
      onReset: () => { t.reset(); setExit(null); doFit() },
      onExit: (code) => setExit(code),
      onState: setState,
    })
    conn.current = c
    const d = t.onData((data) => c.input(data))
    const ro = new ResizeObserver(doFit)
    ro.observe(host.current!)
    return () => { ro.disconnect(); d.dispose(); c.close(); t.dispose(); term.current = null }
  }, [workspaceId, sessionId])

  useEffect(() => { if (visible) { try { fit.current?.fit(); conn.current?.resize(term.current!.cols, term.current!.rows); term.current?.focus() } catch { /* not laid out yet */ } } }, [visible])

  const banner = exit !== null ? `The process exited with code ${exit}.` : STATE_TEXT[state]
  return (
    <div className={visible ? 'relative h-full' : 'hidden'} role="region" aria-label={title}>
      <div ref={host} className="h-full w-full px-2 pt-1" />
      {banner && (
        <div role="status" className="absolute inset-x-0 bottom-0 flex items-center gap-3 border-t border-line bg-panel/95 px-3 py-1.5 text-xs text-muted">
          <span>{banner}</span>
          {state === 'failed' && <Button size="sm" onClick={() => conn.current?.retry()}>Reconnect</Button>}
          {(state === 'ended' || exit !== null) && <Button size="sm" onClick={() => endedRef.current()}>Close terminal</Button>}
        </div>
      )}
    </div>
  )
}
