import { useState } from 'react'
import type { Workspace } from '@/api/types'
import { FileTree } from './FileTree'

/** Code view: file tree, editor and terminal for a running workspace. */
export function CodeView({ workspace }: { workspace: Workspace }) {
  const [active, setActive] = useState<{ path: string; line?: number } | null>(null)

  if (workspace.status !== 'ready') {
    return <Notice title="This workspace is stopped" body="Start the workspace from the dashboard to browse files and open terminals." />
  }
  if (!workspace.agentConnected) {
    return <Notice title="Connecting to the workspace…" body="The workspace agent has not reported in yet. This usually takes a few seconds after the workspace starts." busy />
  }

  return (
    <div className="flex min-h-0 flex-1" aria-label="Code view">
      <aside className="w-[280px] shrink-0 border-r border-line bg-panel">
        <FileTree
          workspaceId={workspace.id}
          activePath={active?.path ?? null}
          onOpen={(path, line) => setActive({ path, line })}
          onPathChanged={(from, to) => setActive((a) => (a && (a.path === from || a.path.startsWith(from + '/')) ? (to ? { path: to + a.path.slice(from.length) } : null) : a))}
        />
      </aside>
      <main className="flex min-w-0 flex-1 flex-col bg-bg">
        <div className="grid flex-1 place-items-center text-sm text-muted">
          {active ? <span className="font-mono">{active.path}</span> : 'Select a file to open it.'}
        </div>
      </main>
    </div>
  )
}

function Notice({ title, body, busy }: { title: string; body: string; busy?: boolean }) {
  return (
    <div role={busy ? 'status' : undefined} className="grid flex-1 place-items-center p-8 text-center">
      <div>
        <p className="text-lg font-bold">{title}</p>
        <p className="mt-1 max-w-sm text-sm text-muted">{body}</p>
      </div>
    </div>
  )
}
