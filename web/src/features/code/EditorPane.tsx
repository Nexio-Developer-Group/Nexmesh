import clsx from 'clsx'
import { lazy, Suspense, useState } from 'react'
import { Button } from '@/components/ui/Button'
import { Modal } from '@/components/ui/Modal'
import { baseOf, downloadUrl } from './hooks'
import { isDirty, type Tab } from './useOpenFiles'

const MonacoEditor = lazy(() => import('./MonacoEditor'))

interface Props {
  workspaceId: string
  tabs: Tab[]
  active: string | null
  onActivate: (p: string) => void
  onClose: (p: string) => void
  onEdit: (p: string, text: string) => void
  onSave: (p: string, force?: boolean) => Promise<boolean>
  onReload: (p: string) => void
}

export function EditorPane({ workspaceId, tabs, active, onActivate, onClose, onEdit, onSave, onReload }: Props) {
  const [closing, setClosing] = useState<Tab | null>(null)
  const tab = tabs.find((t) => t.path === active)
  const requestClose = (t: Tab) => (isDirty(t) ? setClosing(t) : onClose(t.path))

  if (tabs.length === 0) {
    return <div className="grid flex-1 place-items-center text-sm text-muted">Select a file to open it.</div>
  }
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div role="tablist" aria-label="Open files" className="flex shrink-0 overflow-x-auto border-b border-line bg-panel">
        {tabs.map((t) => (
          <div key={t.path} className={clsx('group flex items-center border-r border-line text-[13px]', t.path === active ? 'bg-bg text-fg' : 'text-muted hover:text-fg')}>
            <button role="tab" aria-selected={t.path === active} title={t.path} onClick={() => onActivate(t.path)} className="max-w-[200px] truncate py-2 pl-3 pr-1.5">
              {baseOf(t.path)}
              {isDirty(t) && <span aria-label="unsaved changes" className="ml-1.5 inline-block size-2 rounded-full bg-warn-fg align-middle" />}
            </button>
            <button aria-label={`Close ${baseOf(t.path)}`} onClick={() => requestClose(t)} className="mr-1 rounded px-1.5 text-faint hover:bg-raised hover:text-fg">×</button>
          </div>
        ))}
      </div>

      {tab && (
        <div className="flex min-h-0 flex-1 flex-col" role="tabpanel" aria-label={tab.path}>
          <div className="flex items-center gap-3 border-b border-line px-4 py-1.5 text-xs text-muted">
            <span className="truncate font-mono">/{tab.path}</span>
            {tab.status === 'ready' && (
              <Button size="sm" className="ml-auto" disabled={!isDirty(tab)} loading={tab.saving} onClick={() => onSave(tab.path)}>Save</Button>
            )}
          </div>
          {tab.conflict && (
            <div role="alert" className="flex flex-wrap items-center gap-3 border-b border-warn-fg/40 bg-warn-bg px-4 py-2 text-[13px] text-warn-fg">
              <span><strong>This file changed in the workspace</strong> since you opened it (a terminal, command or another editor).</span>
              <span className="ml-auto flex gap-2">
                <Button size="sm" onClick={() => onReload(tab.path)}>Discard my edits and reload</Button>
                <Button size="sm" variant="danger" onClick={() => onSave(tab.path, true)}>Overwrite with my version</Button>
              </span>
            </div>
          )}
          {tab.error && tab.status === 'ready' && <p role="alert" className="border-b border-line bg-err-bg px-4 py-2 text-[13px] text-err-fg">{tab.error}</p>}
          <div className="min-h-0 flex-1">
            {tab.status === 'loading' && <p role="status" className="p-6 text-sm text-muted">Opening…</p>}
            {tab.status === 'error' && <p role="alert" className="p-6 text-sm text-err-fg">{tab.error}</p>}
            {(tab.status === 'binary' || tab.status === 'toobig') && (
              <div className="grid h-full place-items-center p-6 text-center text-sm text-muted">
                <div>
                  <p className="font-semibold text-fg">{tab.status === 'binary' ? 'This looks like a binary file.' : 'This file is too large to edit here.'}</p>
                  <p className="mt-1">It can still be downloaded.</p>
                  <a className="mt-3 inline-block rounded-[10px] border border-line-strong px-3 py-1.5 font-semibold text-fg hover:border-blue" href={downloadUrl(workspaceId, tab.path)} download={baseOf(tab.path)}>Download</a>
                </div>
              </div>
            )}
            {tab.status === 'ready' && (
              <Suspense fallback={<p role="status" className="p-6 text-sm text-muted">Loading editor…</p>}>
                <MonacoEditor key={workspaceId} path={tab.path} initial={tab.saved} revision={tab.revision} line={tab.line}
                  onChange={(text) => onEdit(tab.path, text)} onSave={() => onSave(tab.path)} />
              </Suspense>
            )}
          </div>
        </div>
      )}

      {closing && (
        <Modal title={`Save changes to ${baseOf(closing.path)}?`} onClose={() => setClosing(null)}
          footer={<>
            <Button onClick={() => setClosing(null)}>Cancel</Button>
            <Button variant="danger" onClick={() => { onClose(closing.path); setClosing(null) }}>Discard</Button>
            <Button variant="primary" onClick={async () => { if (await onSave(closing.path)) { onClose(closing.path); setClosing(null) } else setClosing(null) }}>Save</Button>
          </>}>
          Your edits will be lost if you close without saving.
        </Modal>
      )}
    </div>
  )
}
