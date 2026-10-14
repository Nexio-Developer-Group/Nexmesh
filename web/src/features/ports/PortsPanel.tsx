import { useState } from 'react'
import { ApiError } from '@/api/client'
import { Button } from '@/components/ui/Button'
import { Field } from '@/components/ui/Field'
import { Modal } from '@/components/ui/Modal'
import { usePortActions, usePorts, type PortRow } from './hooks'

/** Open a URL fetched asynchronously without tripping popup blockers. */
export function openInNewTab(getUrl: Promise<string>) {
  const w = window.open('about:blank', '_blank')
  getUrl.then((u) => { if (w) { w.opener = null; w.location.href = u } else window.location.assign(u) }).catch(() => w?.close())
}

export function PortsPanel({ workspaceId }: { workspaceId: string }) {
  const { data, isPending, error } = usePorts(workspaceId)
  const acts = usePortActions(workspaceId)
  const [editing, setEditing] = useState<PortRow | 'new' | null>(null)

  const open = (p: PortRow) => openInNewTab(acts.preview.mutateAsync(p.port).then((r) => r.url))

  return (
    <div className="flex h-full min-h-0 flex-col" aria-label="Ports">
      <div className="flex items-center border-b border-line px-3 py-2">
        <h3 className="text-[11px] font-bold uppercase tracking-wider text-faint">Ports</h3>
        <Button size="sm" variant="ghost" className="ml-auto" onClick={() => setEditing('new')}>Add port</Button>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        {isPending && <p role="status" className="p-4 text-sm text-muted">Looking for open ports…</p>}
        {error instanceof ApiError && <p role="alert" className="p-4 text-sm text-err-fg">{error.message}</p>}
        {data && !data.online && <p className="mx-3 mt-3 rounded-lg bg-warn-bg p-2.5 text-xs text-warn-fg">The workspace is not connected, so listening ports can't be detected right now.</p>}
        {data && data.ports.length === 0 && data.online && (
          <p className="p-4 text-sm text-muted">Nothing is listening yet. Start a dev server in the terminal and it will appear here.</p>
        )}
        <ul>
          {data?.ports.map((p) => (
            <li key={p.port} className="group border-b border-line px-3 py-2.5">
              <div className="flex items-center gap-2">
                <span className="font-mono text-[13px] font-semibold">{p.port}</span>
                {p.label && <span className="truncate text-[13px] text-muted">{p.label}</span>}
                <span className={p.detected ? 'ml-auto rounded-full bg-ok-bg px-2 py-0.5 text-[11px] font-semibold text-ok-fg' : 'ml-auto rounded-full bg-neutral-bg px-2 py-0.5 text-[11px] font-semibold text-muted'}>
                  <span aria-hidden>{p.detected ? '● ' : '○ '}</span>{p.detected ? 'Listening' : 'Not running'}
                </span>
              </div>
              {p.detected && (p.process || p.local) && (
                <p className="mt-0.5 text-[11.5px] text-muted">{p.process}{p.process && p.local ? ' · ' : ''}{p.local ? 'loopback only (still previewable)' : ''}</p>
              )}
              <div className="mt-2 flex gap-1.5">
                <Button size="sm" variant="primary" disabled={!p.detected} loading={acts.preview.isPending && acts.preview.variables === p.port} onClick={() => open(p)} aria-label={`Open preview of port ${p.port}`}>Open preview ↗</Button>
                <Button size="sm" variant="ghost" onClick={() => setEditing(p)} aria-label={`Edit port ${p.port}`}>{p.declared ? 'Rename' : 'Name it'}</Button>
                {p.declared && <Button size="sm" variant="ghost" aria-label={`Remove port ${p.port}`} onClick={() => acts.remove.mutate(p.port)}>Remove</Button>}
              </div>
            </li>
          ))}
        </ul>
        {acts.preview.error instanceof ApiError && <p role="alert" className="p-3 text-xs text-err-fg">{acts.preview.error.message}</p>}
        {data?.previewMode === 'path' && (
          <p className="m-3 rounded-lg border border-line p-2.5 text-[11.5px] leading-relaxed text-muted">
            Previews open in a sandbox for safety. Apps that need cookies or local storage work best with a preview domain (<code className="font-mono">NEXMESH_PREVIEW_DOMAIN</code>).
          </p>
        )}
      </div>
      {editing && <PortDialog workspaceId={workspaceId} port={editing === 'new' ? null : editing} onClose={() => setEditing(null)} />}
    </div>
  )
}

function PortDialog({ workspaceId, port, onClose }: { workspaceId: string; port: PortRow | null; onClose: () => void }) {
  const { declare } = usePortActions(workspaceId)
  const [num, setNum] = useState(port ? String(port.port) : '')
  const [label, setLabel] = useState(port?.label ?? '')
  const err = declare.error instanceof ApiError ? declare.error : null
  const n = Number(num)
  const valid = Number.isInteger(n) && n >= 1 && n <= 65535
  return (
    <Modal title={port ? `Name port ${port.port}` : 'Add a port'} onClose={onClose}
      footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" type="submit" form="port-form" disabled={!valid} loading={declare.isPending}>Save</Button></>}>
      <form id="port-form" className="space-y-4 text-fg" onSubmit={(e) => { e.preventDefault(); declare.mutate({ port: n, label }, { onSuccess: onClose }) }}>
        <Field label="Port" mono inputMode="numeric" value={num} disabled={!!port} onChange={(e) => setNum(e.target.value.replace(/\D/g, ''))}
          error={num && !valid ? 'Enter a port between 1 and 65535.' : err?.field === 'port' ? err.message : undefined} hint="Declared ports are listed even when nothing is running on them." />
        <Field label="Label" optional value={label} onChange={(e) => setLabel(e.target.value)} placeholder="web, api, storybook…" error={err?.field === 'label' ? err.message : undefined} />
        {err && !err.field && <p role="alert" className="text-sm text-err-fg">{err.message}</p>}
      </form>
    </Modal>
  )
}
