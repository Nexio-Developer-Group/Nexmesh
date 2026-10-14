import type { Workspace } from '@/api/types'
import { useState } from 'react'
import { GitPanel } from '@/features/git/GitPanel'
import { EditorPane } from './EditorPane'
import { FileTree } from './FileTree'
import { TerminalPanel } from './terminal/TerminalPanel'
import { useOpenFiles } from './useOpenFiles'

/** Code view: file tree, editor and terminal for a running workspace. */
export function CodeView({ workspace }: { workspace: Workspace }) {
  const files = useOpenFiles(workspace.id)
  const [side, setSide] = useState<'files' | 'git'>('files')

  if (workspace.status !== 'ready') {
    return <Notice title="This workspace is stopped" body="Start the workspace from the dashboard to browse files and open terminals." />
  }
  if (!workspace.agentConnected) {
    return <Notice title="Connecting to the workspace…" body="The workspace agent has not reported in yet. This usually takes a few seconds after the workspace starts." busy />
  }

  return (
    <div className="flex min-h-0 flex-1" aria-label="Code view">
      <aside className="flex w-[300px] shrink-0 flex-col border-r border-line bg-panel">
        <div role="tablist" aria-label="Sidebar" className="flex shrink-0 border-b border-line text-[13px]">
          {(['files', 'git'] as const).map((t) => (
            <button key={t} role="tab" aria-selected={side === t} onClick={() => setSide(t)}
              className={side === t ? 'flex-1 border-b-2 border-blue py-2 font-semibold text-fg' : 'flex-1 py-2 text-muted hover:text-fg'}>{t === 'files' ? 'Files' : 'Source control'}</button>
          ))}
        </div>
        <div className="min-h-0 flex-1">
        {side === 'git' ? <GitPanel workspaceId={workspace.id} onOpenFile={(repo, path) => files.open(repo ? `${repo}/${path}` : path)} /> :
        <FileTree
          workspaceId={workspace.id}
          activePath={files.active}
          onOpen={files.open}
          onPathChanged={files.pathChanged}
        />}
        </div>
      </aside>
      <main className="flex min-w-0 flex-1 flex-col bg-bg">
        <EditorPane
          workspaceId={workspace.id}
          tabs={files.tabs}
          active={files.active}
          onActivate={(p) => { files.setActive(p); void files.checkDisk(p) }}
          onClose={files.close}
          onEdit={files.edit}
          onSave={files.save}
          onReload={files.reload}
        />
        <TerminalPanel workspaceId={workspace.id} />
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
