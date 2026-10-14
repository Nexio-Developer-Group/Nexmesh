import { useState } from 'react'
import { ApiError } from '@/api/client'
import type { Workspace } from '@/api/types'
import { Button } from '@/components/ui/Button'
import { Field } from '@/components/ui/Field'
import { Modal } from '@/components/ui/Modal'
import { useSnapshotActions, useSnapshots, type Snapshot } from './hooks'

function size(b: number) {
  if (b >= 1 << 30) return `${(b / (1 << 30)).toFixed(1)} GB`
  if (b >= 1 << 20) return `${(b / (1 << 20)).toFixed(1)} MB`
  return `${Math.max(1, Math.round(b / 1024))} KB`
}

export function SnapshotsTab({ workspace }: { workspace: Workspace }) {
  const { data, isPending, error } = useSnapshots(workspace.id)
  const acts = useSnapshotActions(workspace.id)
  const [creating, setCreating] = useState(false)
  const [restoring, setRestoring] = useState<Snapshot | null>(null)
  const [deleting, setDeleting] = useState<Snapshot | null>(null)
  const stopped = workspace.status === 'stopped'
  const full = !!data && data.snapshots.length >= data.max

  return (
    <section aria-label="Snapshots" className="max-w-3xl space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-2xl font-extrabold tracking-tight">Snapshots</h2>
          <p className="mt-1 max-w-xl text-muted">Save a copy of this workspace's files and restore it later, for example before a risky change. Installed services and their data are not included.</p>
        </div>
        <Button variant="primary" size="sm" disabled={full || !['ready', 'stopped'].includes(workspace.status)} onClick={() => setCreating(true)}>Create snapshot</Button>
      </div>
      {full && <p className="text-sm text-muted">This workspace has {data!.max} snapshots, the most it can keep. Delete one to make room.</p>}
      {error instanceof ApiError && <p role="alert" className="rounded-lg bg-err-bg p-3 text-sm text-err-fg">{error.message}</p>}
      {isPending && <p className="text-muted">Loading…</p>}
      {data && data.snapshots.length === 0 && <p className="rounded-card border border-dashed border-line-strong p-6 text-center text-sm text-muted">No snapshots yet.</p>}
      {data && data.snapshots.length > 0 && (
        <ul className="divide-y divide-line overflow-hidden rounded-card border border-line bg-panel">
          {[...data.snapshots].reverse().map((s) => (
            <li key={s.id} className="flex flex-wrap items-center gap-3 px-4 py-3">
              <div className="min-w-0 flex-1">
                <p className="truncate font-semibold">{s.name}</p>
                <p className="text-xs text-muted">
                  {new Date(s.createdAt).toLocaleString()}
                  {s.state === 'ready' && ` · ${size(s.sizeBytes)}`}
                  {s.state === 'creating' && ' · saving…'}
                </p>
                {s.state === 'failed' && <p role="alert" className="mt-1 text-xs text-err-fg">Failed: {s.error || 'the snapshot could not be saved.'}</p>}
              </div>
              <Button size="sm" variant="ghost" aria-label={`Restore ${s.name}`} disabled={s.state !== 'ready' || !stopped} title={!stopped ? 'Stop the workspace to restore a snapshot' : undefined} onClick={() => setRestoring(s)}>Restore</Button>
              <Button size="sm" variant="ghost" aria-label={`Delete ${s.name}`} disabled={s.state === 'creating'} onClick={() => setDeleting(s)}>Delete</Button>
            </li>
          ))}
        </ul>
      )}
      {data && data.snapshots.some((s) => s.state === 'ready') && !stopped && <p className="text-xs text-muted">To restore, stop the workspace first so no files change while they are replaced.</p>}

      {creating && <CreateDialog workspaceId={workspace.id} onClose={() => setCreating(false)} />}
      {restoring && (
        <Modal title={`Restore “${restoring.name}”?`} onClose={() => setRestoring(null)}
          footer={<><Button onClick={() => setRestoring(null)}>Cancel</Button>
            <Button variant="danger" loading={acts.restore.isPending} onClick={() => acts.restore.mutate(restoring.id, { onSuccess: () => setRestoring(null) })}>Replace files with this snapshot</Button></>}>
          Every file in the workspace is replaced with the snapshot's. Changes made since {new Date(restoring.createdAt).toLocaleString()} are lost. Start the workspace afterwards to resume.
          {acts.restore.error instanceof ApiError && <span role="alert" className="mt-2 block text-err-fg">{acts.restore.error.message}</span>}
        </Modal>
      )}
      {deleting && (
        <Modal title={`Delete “${deleting.name}”?`} onClose={() => setDeleting(null)}
          footer={<><Button onClick={() => setDeleting(null)}>Cancel</Button>
            <Button variant="danger" loading={acts.remove.isPending} onClick={() => acts.remove.mutate(deleting.id, { onSuccess: () => setDeleting(null) })}>Delete snapshot</Button></>}>
          The saved copy is removed permanently. The workspace itself is not changed.
        </Modal>
      )}
    </section>
  )
}

function CreateDialog({ workspaceId, onClose }: { workspaceId: string; onClose: () => void }) {
  const { create } = useSnapshotActions(workspaceId)
  const [name, setName] = useState('')
  const err = create.error instanceof ApiError ? create.error : null
  return (
    <Modal title="Create snapshot" onClose={onClose}
      footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" type="submit" form="snap-form" disabled={!name.trim()} loading={create.isPending}>Create snapshot</Button></>}>
      <form id="snap-form" className="space-y-3 text-fg" onSubmit={(e) => { e.preventDefault(); create.mutate(name, { onSuccess: onClose }) }}>
        <Field label="Name" value={name} onChange={(e) => setName(e.target.value)} placeholder="Before refactor" error={err?.field === 'name' ? err.message : undefined} maxLength={60} />
        {err && err.field !== 'name' && <p role="alert" className="text-sm text-err-fg">{err.message}</p>}
        <p className="text-xs text-muted">The workspace can keep running while the snapshot is saved.</p>
      </form>
    </Modal>
  )
}
