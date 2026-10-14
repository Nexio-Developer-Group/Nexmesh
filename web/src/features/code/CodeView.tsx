import type { Workspace } from '@/api/types'
import { EditorPane } from './EditorPane'
import { FileTree } from './FileTree'
import { TerminalPanel } from './terminal/TerminalPanel'
import { useOpenFiles } from './useOpenFiles'

/** Code view: file tree, editor and terminal for a running workspace. */
export function CodeView({ workspace }: { workspace: Workspace }) {
  const files = useOpenFiles(workspace.id)

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
          activePath={files.active}
          onOpen={files.open}
          onPathChanged={files.pathChanged}
        />
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
