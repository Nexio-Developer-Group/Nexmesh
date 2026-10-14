import clsx from 'clsx'
import { useEffect, useRef, useState } from 'react'
import { ApiError } from '@/api/client'
import { useTerminalActions, useTerminalList } from './hooks'
import { TerminalView } from './TerminalView'

/** Bottom panel with one tab per persistent terminal session. */
export function TerminalPanel({ workspaceId }: { workspaceId: string }) {
  const list = useTerminalList(workspaceId)
  const { open, close } = useTerminalActions(workspaceId)
  const [active, setActive] = useState<string | null>(null)
  const [collapsed, setCollapsed] = useState(false)
  const started = useRef(false)

  const sessions = (list.data ?? []).filter((s) => !s.exited || s.id === active)

  // First visit: reuse a surviving session (it outlived the last browser tab) or start one.
  useEffect(() => {
    if (!list.isSuccess || started.current) return
    started.current = true
    const live = list.data.filter((s) => !s.exited)
    if (live.length) setActive(live[0].id)
    else open.mutate({}, { onSuccess: (s) => setActive(s.id) })
  }, [list.isSuccess, list.data, open])

  const add = () => open.mutate({}, { onSuccess: (s) => { setActive(s.id); setCollapsed(false) } })
  const end = (id: string) => close.mutate(id, { onSuccess: () => setActive((a) => (a === id ? sessions.find((s) => s.id !== id)?.id ?? null : a)) })
  const err = [list.error, open.error, close.error].find(Boolean)

  return (
    <section aria-label="Terminal" className={clsx('flex shrink-0 flex-col border-t border-line bg-bg', collapsed ? 'h-9' : 'h-[300px]')}>
      <div className="flex h-9 shrink-0 items-center border-b border-line bg-panel text-[13px]">
        <div role="tablist" aria-label="Terminals" className="flex min-w-0 flex-1 overflow-x-auto">
          {sessions.map((s) => (
            <div key={s.id} className={clsx('flex items-center border-r border-line', s.id === active ? 'bg-bg text-fg' : 'text-muted hover:text-fg')}>
              <button role="tab" aria-selected={s.id === active} onClick={() => { setActive(s.id); setCollapsed(false) }} className="max-w-[160px] truncate py-1.5 pl-3 pr-1.5">{s.title}</button>
              <button aria-label={`Close ${s.title}`} onClick={() => end(s.id)} className="mr-1 rounded px-1.5 text-faint hover:bg-raised hover:text-fg">×</button>
            </div>
          ))}
          <button aria-label="New terminal" title="New terminal" onClick={add} disabled={open.isPending} className="px-3 text-muted hover:text-fg disabled:opacity-50">+</button>
        </div>
        <button aria-label={collapsed ? 'Expand terminal panel' : 'Collapse terminal panel'} aria-expanded={!collapsed} onClick={() => setCollapsed((c) => !c)} className="px-3 text-muted hover:text-fg">{collapsed ? '▴' : '▾'}</button>
      </div>
      <div className={clsx('min-h-0 flex-1', collapsed && 'hidden')}>
        {err && <p role="alert" className="p-3 text-sm text-err-fg">{err instanceof ApiError ? err.message : 'Terminal error.'}</p>}
        {list.isPending && <p role="status" className="p-3 text-sm text-muted">Starting terminal…</p>}
        {sessions.map((s) => (
          <TerminalView key={s.id} workspaceId={workspaceId} sessionId={s.id} title={s.title} visible={s.id === active && !collapsed} onEnded={() => end(s.id)} />
        ))}
      </div>
    </section>
  )
}
