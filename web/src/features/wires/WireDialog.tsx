import { useEffect, useState } from 'react'
import type { CanvasComponent, Wire, WirePreview, WireState } from '@/api/types'
import { Modal } from '@/components/ui/Modal'
import { Button } from '@/components/ui/Button'
import { Field } from '@/components/ui/Field'
import { useCreateWire, useDeleteWire, usePreviewWire } from './hooks'

export const WIRE_STATE: Record<WireState, { label: string; cls: string }> = {
  healthy: { label: 'Healthy', cls: 'bg-ok-bg text-ok-fg' },
  degraded: { label: 'Degraded', cls: 'bg-warn-bg text-warn-fg' },
  unreachable: { label: 'Unreachable', cls: 'bg-err-bg text-err-fg' },
  pending: { label: 'Waiting for provider', cls: 'bg-neutral-bg text-neutral-fg' },
}

export function WireStateBadge({ state }: { state: WireState }) {
  const m = WIRE_STATE[state]
  const icon = state === 'healthy' ? '●' : state === 'degraded' ? '▲' : state === 'unreachable' ? '✕' : '○'
  return <span className={`inline-flex h-[22px] items-center gap-1.5 rounded-md px-2 text-[11.5px] font-semibold ${m.cls}`}><span aria-hidden>{icon}</span>{m.label}</span>
}

function VarList({ vars }: { vars: { name: string; secret?: boolean; conflict?: string }[] }) {
  return (
    <ul aria-label="Variables" className="mt-2 space-y-1 rounded-[10px] border border-line bg-field p-3 font-mono text-[12px] text-fg">
      {vars.map((v) => (
        <li key={v.name} className="flex items-center justify-between gap-3">
          <span>{v.name}</span>
          {v.secret && <span className="text-[10.5px] font-sans text-faint">contains a secret, value hidden</span>}
        </li>
      ))}
    </ul>
  )
}

/** Review a new line before it exists: nothing is created until Confirm (PRD F1). */
export function NewWireDialog({ workspaceId, from, to, onClose }: { workspaceId: string; from: CanvasComponent; to: CanvasComponent; onClose: () => void }) {
  const preview = usePreviewWire(workspaceId)
  const create = useCreateWire(workspaceId)
  const [name, setName] = useState('')
  const run = (n: string) => preview.mutate({ fromId: from.id, toId: to.id, name: n || undefined })
  useEffect(() => { run('') }, []) // eslint-disable-line react-hooks/exhaustive-deps
  const pv: WirePreview | undefined = preview.data
  const err = preview.error?.message ?? create.error?.message
  return (
    <Modal title="Connect cards" onClose={onClose} width={520}
      footer={<>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" disabled={!pv || !!preview.error} loading={create.isPending}
          onClick={() => create.mutate({ fromId: from.id, toId: to.id, name: name || undefined }, { onSuccess: onClose })}>Create connection</Button>
      </>}>
      {preview.isPending && !pv && <p>Checking the connection…</p>}
      {err && <p role="alert" className="rounded-lg bg-err-bg px-3 py-2 text-err-fg">{err}</p>}
      {pv && (
        <div className="space-y-3">
          <p className="text-fg"><strong>{pv.fromName}</strong> will receive these variables from <strong>{pv.toName}</strong>:</p>
          <VarList vars={pv.variables} />
          <Field label="Connection name" mono value={name} placeholder={pv.name} hint="Variable names start with this, in capitals."
            onChange={(e) => setName(e.target.value)} onBlur={() => run(name)} />
          {pv.warnings.map((w) => <p key={w} className="rounded-lg bg-warn-bg px-3 py-2 text-[13px] text-warn-fg">{w}</p>)}
          <p className="text-[12.5px] text-faint">Only the names are stored. Values (including passwords) are read from the secrets store when a process starts and are never shown here.</p>
        </div>
      )}
    </Modal>
  )
}

/** Inspect or remove an existing line. */
export function WireDetailsDialog({ workspaceId, wire, fromName, toName, onClose }: { workspaceId: string; wire: Wire; fromName: string; toName: string; onClose: () => void }) {
  const del = useDeleteWire(workspaceId)
  return (
    <Modal title="Connection" onClose={onClose} width={520}
      footer={<>
        <Button onClick={onClose}>Close</Button>
        <Button variant="danger" loading={del.isPending} onClick={() => del.mutate(wire.id, { onSuccess: onClose })}>Remove connection</Button>
      </>}>
      <div className="space-y-3">
        <p className="text-fg"><strong>{fromName}</strong> → <strong>{toName}</strong> <WireStateBadge state={wire.state} /></p>
        {wire.message && <p>{wire.message}</p>}
        <p>Variables this connection provides:</p>
        <VarList vars={wire.variables.map((name) => ({ name }))} />
        <p className="text-[12.5px] text-faint">Removing the connection removes these variables from the environment. Nothing is deleted from either card.</p>
        {del.error && <p role="alert" className="text-err-fg">{del.error.message}</p>}
      </div>
    </Modal>
  )
}
