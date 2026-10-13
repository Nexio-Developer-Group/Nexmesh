import { useState } from 'react'
import { ApiError } from '@/api/client'
import type { CatalogComponent, Operation, Workspace } from '@/api/types'
import { Button } from '@/components/ui/Button'
import { Modal } from '@/components/ui/Modal'
import { StatusBadge } from '@/components/ui/StatusBadge'
import { useOperations, useRuntimeInstall } from './hooks'

const RUNTIMES = ['python', 'nodejs', 'go']

function RuntimeCard({ workspaceId, def, state, ops }: { workspaceId: string; def: CatalogComponent; state?: Workspace['runtimes'][string]; ops: Operation[] }) {
  const installed = state?.installed ?? []
  const available = def.versions.supported.filter((v) => !installed.includes(v))
  const [version, setVersion] = useState(available[0] ?? '')
  const [activate, setActivate] = useState(true)
  const install = useRuntimeInstall(workspaceId)
  const latest = ops.find((o) => o.type === 'install_runtime' && o.params?.name === def.id)
  const running = latest && (latest.status === 'running' || latest.status === 'pending')
  const err = install.error instanceof ApiError ? install.error.message : undefined

  return (
    <section aria-label={`${def.name} runtime`} className="rounded-xl border border-line p-4">
      <div className="flex items-center justify-between">
        <h3 className="font-bold">{def.name}</h3>
        {state?.active ? <span className="font-mono text-xs text-ok-fg">{state.active} active</span> : <span className="font-mono text-xs text-faint">not installed</span>}
      </div>
      {installed.length > 0 && <p className="mt-1 font-mono text-xs text-muted">Installed: {installed.join(', ')}</p>}
      {available.length > 0 ? (
        <div className="mt-3 flex flex-wrap items-center gap-2">
          <label className="sr-only" htmlFor={`rv-${def.id}`}>{def.name} version</label>
          <select id={`rv-${def.id}`} value={version || available[0]} onChange={(e) => setVersion(e.target.value)} className="h-9 rounded-lg border border-line-strong bg-field px-2 font-mono text-sm">
            {available.map((v) => <option key={v}>{v}</option>)}
          </select>
          <label className="flex items-center gap-1.5 text-xs text-muted"><input type="checkbox" checked={activate} onChange={(e) => setActivate(e.target.checked)} className="accent-blue" /> Make active</label>
          <Button size="sm" variant="primary" className="ml-auto" loading={install.isPending || !!running}
            onClick={() => install.mutate({ name: def.id, version: version || available[0], activate })}>Install version</Button>
        </div>
      ) : <p className="mt-3 text-xs text-muted">All supported versions are installed.</p>}
      {err && <p role="alert" className="mt-2 text-sm text-err-fg">{err}</p>}
      {latest && (
        <div className="mt-3">
          <div className="flex items-center gap-2 text-xs">
            <StatusBadge state={latest.status === 'succeeded' ? 'ready' : latest.status === 'failed' ? 'failed' : 'installing'} label={latest.status === 'succeeded' ? 'Installed' : latest.status === 'failed' ? 'Failed' : `Installing ${latest.progress}%`} />
          </div>
          {latest.status === 'failed' && (
            <pre aria-label={`${def.name} install log`} className="mt-2 max-h-40 overflow-auto rounded-lg bg-night p-2.5 font-mono text-[11px] text-night-text">{[...latest.log, `error: ${latest.error}`].join('\n')}</pre>
          )}
        </div>
      )}
    </section>
  )
}

/** Global runtimes (PRD 9): install versions and see which one is active. */
export function RuntimesPanel({ workspace, defs, onClose }: { workspace: Workspace; defs: Map<string, CatalogComponent>; onClose: () => void }) {
  const ops = useOperations(workspace.id)
  return (
    <Modal title="Runtimes" onClose={onClose} width={560} footer={<Button onClick={onClose}>Close</Button>}>
      <p className="mb-4">Install Python, Node.js and Go at specific versions. The active version is recorded in the workspace configuration; projects pin the version they need.</p>
      <div className="space-y-3">
        {RUNTIMES.map((id) => defs.get(id) && <RuntimeCard key={id} workspaceId={workspace.id} def={defs.get(id)!} state={workspace.runtimes?.[id]} ops={ops.data ?? []} />)}
      </div>
    </Modal>
  )
}
