import { useState } from 'react'
import { ApiError } from '@/api/client'
import type { Workspace } from '@/api/types'
import { Button } from '@/components/ui/Button'
import { Field } from '@/components/ui/Field'
import { osLabel } from '@/lib/format'
import { useApplySettings } from './hooks'

const IDLE_OPTIONS: [number, string][] = [[0, 'Never'], [15, 'After 15 minutes'], [30, 'After 30 minutes'], [60, 'After 1 hour'], [120, 'After 2 hours'], [240, 'After 4 hours'], [480, 'After 8 hours']]

export function GeneralTab({ workspace: w }: { workspace: Workspace }) {
  const [name, setName] = useState(w.name)
  const [description, setDescription] = useState(w.description)
  const apply = useApplySettings(w.id)
  const [idle, setIdle] = useState(w.idleStopMinutes ?? 0)
  const dirty = name !== w.name || description !== w.description || idle !== (w.idleStopMinutes ?? 0)
  const err = apply.error instanceof ApiError ? apply.error : null
  return (
    <section aria-label="General settings" className="max-w-2xl space-y-6">
      <div>
        <h2 className="text-2xl font-extrabold tracking-tight">General</h2>
        <p className="mt-1 text-muted">Name and describe this workspace. These apply immediately.</p>
      </div>
      <Field label="Workspace name" value={name} onChange={(e) => setName(e.target.value.toLowerCase())} error={err?.field === 'name' ? err.message : undefined} />
      <Field label="Description" optional value={description} onChange={(e) => setDescription(e.target.value)} />
      <div>
        <p className="mb-1.5 text-[13px] font-semibold">Operating system</p>
        <div className="flex items-center gap-4 rounded-xl border border-line bg-panel p-3.5">
          <span aria-hidden className="grid size-10 place-items-center rounded-lg border border-line bg-raised font-mono text-xs font-bold text-ubuntu">Ub</span>
          <div className="flex-1">
            <p className="font-semibold" id="os-name">{osLabel(w.os)}</p>
            <p className="font-mono text-[11px] text-muted">noble · x86_64</p>
          </div>
          <span className="rounded-md bg-neutral-bg px-2.5 py-1 text-xs font-medium text-muted"><span aria-hidden>🔒 </span>Locked</span>
        </div>
        <p className="mt-2 text-sm text-muted">Changing the operating system requires creating a new workspace.</p>
      </div>
      <div>
        <label htmlFor="idle-stop" className="mb-1.5 block text-[13px] font-semibold">Stop when idle</label>
        <select id="idle-stop" value={idle} onChange={(e) => setIdle(Number(e.target.value))} className="h-10 w-full rounded-[10px] border border-line-strong bg-field px-3 text-sm">
          {IDLE_OPTIONS.map(([m, l]) => <option key={m} value={m}>{l}</option>)}
        </select>
        <p className="mt-1.5 text-xs text-muted">Frees CPU and memory when nobody is using the workspace. Files and installed services are kept; start it again to resume. It never stops while a terminal has a process running or the workspace is open in a browser.</p>
        {err?.field === 'idleStopMinutes' && <p role="alert" className="mt-1 text-xs text-err-fg">{err.message}</p>}
      </div>
      {err && err.field !== 'name' && err.field !== 'idleStopMinutes' && <p role="alert" className="rounded-lg bg-err-bg p-3 text-sm text-err-fg">{err.message}</p>}
      <div className="flex items-center gap-3">
        <Button variant="primary" disabled={!dirty} loading={apply.isPending} onClick={() => apply.mutate({
          ...(name !== w.name ? { name } : {}),
          ...(description !== w.description ? { description } : {}),
          ...(idle !== (w.idleStopMinutes ?? 0) ? { idleStopMinutes: idle } : {}),
        })}>Save changes</Button>
        {apply.isSuccess && !dirty && <span role="status" className="text-sm text-ok-fg">✓ Saved</span>}
      </div>
    </section>
  )
}
