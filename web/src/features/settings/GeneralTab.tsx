import { useState } from 'react'
import { ApiError } from '@/api/client'
import type { Workspace } from '@/api/types'
import { Button } from '@/components/ui/Button'
import { Field } from '@/components/ui/Field'
import { osLabel } from '@/lib/format'
import { useApplySettings } from './hooks'

export function GeneralTab({ workspace: w }: { workspace: Workspace }) {
  const [name, setName] = useState(w.name)
  const [description, setDescription] = useState(w.description)
  const apply = useApplySettings(w.id)
  const dirty = name !== w.name || description !== w.description
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
          <span aria-hidden className="grid size-10 place-items-center rounded-lg border border-line bg-raised font-mono text-xs font-bold text-[#E95420]">Ub</span>
          <div className="flex-1">
            <p className="font-semibold" id="os-name">{osLabel(w.os)}</p>
            <p className="font-mono text-[11px] text-muted">noble · x86_64</p>
          </div>
          <span className="rounded-md bg-neutral-bg px-2.5 py-1 text-xs font-medium text-muted"><span aria-hidden>🔒 </span>Locked</span>
        </div>
        <p className="mt-2 text-sm text-muted">Changing the operating system requires creating a new workspace.</p>
      </div>
      {err && err.field !== 'name' && <p role="alert" className="rounded-lg bg-err-bg p-3 text-sm text-err-fg">{err.message}</p>}
      <div className="flex items-center gap-3">
        <Button variant="primary" disabled={!dirty} loading={apply.isPending} onClick={() => apply.mutate({ name, description })}>Save changes</Button>
        {apply.isSuccess && !dirty && <span role="status" className="text-sm text-ok-fg">✓ Saved</span>}
      </div>
    </section>
  )
}
