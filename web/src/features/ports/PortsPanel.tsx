import { useState } from 'react'
import { ApiError } from '@/api/client'
import { Button } from '@/components/ui/Button'
import { Field } from '@/components/ui/Field'
import { Modal } from '@/components/ui/Modal'
import { usePortActions, usePorts, useShareActions, useShares, type PortRow } from './hooks'

/** Open a URL fetched asynchronously without tripping popup blockers. */
export function openInNewTab(getUrl: Promise<string>) {
  const w = window.open('about:blank', '_blank')
  getUrl.then((u) => { if (w) { w.opener = null; w.location.href = u } else window.location.assign(u) }).catch(() => w?.close())
}

export function PortsPanel({ workspaceId }: { workspaceId: string }) {
  const { data, isPending, error } = usePorts(workspaceId)
  const acts = usePortActions(workspaceId)
  const [editing, setEditing] = useState<PortRow | 'new' | null>(null)
  const [sharing, setSharing] = useState<PortRow | null>(null)
  const shares = useShares(workspaceId)
  const shareActs = useShareActions(workspaceId)

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
                {shares.data?.enabled && <Button size="sm" variant="ghost" disabled={!p.detected} onClick={() => setSharing(p)} aria-label={`Share port ${p.port}`}>Share…</Button>}
                <Button size="sm" variant="ghost" onClick={() => setEditing(p)} aria-label={`Edit port ${p.port}`}>{p.declared ? 'Rename' : 'Name it'}</Button>
                {p.declared && <Button size="sm" variant="ghost" aria-label={`Remove port ${p.port}`} onClick={() => acts.remove.mutate(p.port)}>Remove</Button>}
              </div>
            </li>
          ))}
        </ul>
        {shares.data && shares.data.links.some((l) => l.active) && (
          <section aria-label="Shared links" className="border-t border-line px-3 py-3">
            <h3 className="mb-2 text-[11px] font-bold uppercase tracking-wider text-faint">Shared links</h3>
            <ul className="space-y-2">
              {shares.data.links.filter((l) => l.active).map((l) => (
                <li key={l.id} className="flex items-center gap-2 text-[12.5px]">
                  <span className="min-w-0 flex-1">
                    <span className="font-mono font-semibold">:{l.port}</span> {l.label && <span className="text-muted">{l.label}</span>}
                    <span className="block text-[11px] text-muted">Expires {new Date(l.expiresAt).toLocaleString()} · opened {l.hits}×</span>
                  </span>
                  <Button size="sm" variant="ghost" aria-label={`Revoke link for port ${l.port}`} loading={shareActs.revoke.isPending && shareActs.revoke.variables === l.id} onClick={() => shareActs.revoke.mutate(l.id)}>Revoke</Button>
                </li>
              ))}
            </ul>
          </section>
        )}
        {shares.data && !shares.data.enabled && (
          <p className="mx-3 mt-3 text-[11.5px] text-faint">Public share links are turned off on this instance.</p>
        )}
        {acts.preview.error instanceof ApiError && <p role="alert" className="p-3 text-xs text-err-fg">{acts.preview.error.message}</p>}
        {data?.previewMode === 'path' && (
          <p className="m-3 rounded-lg border border-line p-2.5 text-[11.5px] leading-relaxed text-muted">
            Previews open in a sandbox for safety. Apps that need cookies or local storage work best with a preview domain (<code className="font-mono">NEXMESH_PREVIEW_DOMAIN</code>).
          </p>
        )}
      </div>
      {sharing && <ShareDialog workspaceId={workspaceId} port={sharing} onClose={() => setSharing(null)} />}
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

const DURATIONS = [[60, '1 hour'], [24 * 60, '24 hours'], [7 * 24 * 60, '7 days']] as const

function ShareDialog({ workspaceId, port, onClose }: { workspaceId: string; port: PortRow; onClose: () => void }) {
  const { create } = useShareActions(workspaceId)
  const [ttl, setTtl] = useState<number>(60)
  const [label, setLabel] = useState('')
  const [copied, setCopied] = useState(false)
  const err = create.error instanceof ApiError ? create.error : null
  const url = create.data?.url
  const copy = async () => {
    try { await navigator.clipboard.writeText(url!); setCopied(true) } catch { /* clipboard unavailable: the field is selectable */ }
  }
  return (
    <Modal title={`Share port ${port.port}`} onClose={onClose}
      footer={url ? <Button variant="primary" onClick={onClose}>Done</Button> : <>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" type="submit" form="share-form" loading={create.isPending}>Create link</Button></>}>
      {url ? (
        <div className="space-y-3 text-fg">
          <p>Anyone with this link can open this port until it expires or you revoke it. <strong>It won't be shown again.</strong></p>
          <div className="flex gap-2">
            <input readOnly aria-label="Share link" value={url} onFocus={(e) => e.currentTarget.select()} className="h-10 min-w-0 flex-1 rounded-[10px] border border-line-strong bg-field px-3 font-mono text-xs" />
            <Button onClick={copy}>{copied ? 'Copied' : 'Copy'}</Button>
          </div>
        </div>
      ) : (
        <form id="share-form" className="space-y-4 text-fg" onSubmit={(e) => { e.preventDefault(); create.mutate({ port: port.port, ttlMinutes: ttl, label }) }}>
          <p className="rounded-lg bg-warn-bg p-2.5 text-xs text-warn-fg">Anyone who has the link can view what's running on this port, without signing in. Only share what you're happy to make public.</p>
          <div>
            <label htmlFor="share-ttl" className="mb-1.5 block text-[13px] font-semibold">Link expires after</label>
            <select id="share-ttl" value={ttl} onChange={(e) => setTtl(Number(e.target.value))} className="h-10 w-full rounded-[10px] border border-line-strong bg-field px-3 text-sm">
              {DURATIONS.map(([m, l]) => <option key={m} value={m}>{l}</option>)}
            </select>
          </div>
          <Field label="Label" optional value={label} onChange={(e) => setLabel(e.target.value)} placeholder="Demo for Sam" error={err?.field === 'label' ? err.message : undefined} />
          {err && err.field !== 'label' && <p role="alert" className="text-sm text-err-fg">{err.message}</p>}
        </form>
      )}
    </Modal>
  )
}
