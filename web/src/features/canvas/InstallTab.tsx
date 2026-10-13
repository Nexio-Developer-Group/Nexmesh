import { useMemo, useState } from 'react'
import { ApiError } from '@/api/client'
import type { CanvasComponent, CatalogComponent, Operation } from '@/api/types'
import { Button } from '@/components/ui/Button'
import { Field } from '@/components/ui/Field'
import { Modal } from '@/components/ui/Modal'
import {
  useCapacity, useComponentAction, useInstallComponent, useOperations, useRemoveInstalled,
} from './hooks'

const fmtRam = (mb: number) => (mb >= 1024 ? `${(mb / 1024).toFixed(mb % 1024 ? 1 : 0)} GB` : `${mb} MB`)

function Spec({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg bg-neutral-bg px-3 py-2">
      <p className="font-mono text-[13px] font-semibold">{value}</p>
      <p className="text-[11px] text-muted">{label}</p>
    </div>
  )
}

/** Install tab: version, settings, prerequisites, and an explicit confirmation (PRD 10, 11.3). */
export function InstallTab({ workspaceId, component: c, def }: { workspaceId: string; component: CanvasComponent; def: CatalogComponent }) {
  const isDraft = c.observedState === 'draft'
  const failedInstall = c.observedState === 'failed' && c.mode === 'installed'
  const showForm = isDraft || failedInstall
  return showForm ? (
    <InstallForm workspaceId={workspaceId} component={c} def={def} retry={failedInstall} />
  ) : (
    <InstalledView workspaceId={workspaceId} component={c} def={def} />
  )
}

function InstallForm({ workspaceId, component: c, def, retry }: { workspaceId: string; component: CanvasComponent; def: CatalogComponent; retry: boolean }) {
  const defaultPort = def.ports?.[0] ?? def.install?.ports?.[0]
  const volume = def.install?.volumes?.[0]
  const [version, setVersion] = useState(c.version || def.versions.default)
  const [port, setPort] = useState<number | ''>(defaultPort ?? '')
  const [volumeGb, setVolumeGb] = useState<number | ''>(volume?.sizeGB ?? '')
  const [confirm, setConfirm] = useState(false)
  const install = useInstallComponent(workspaceId, c.id)
  const retryAction = useComponentAction(workspaceId, c.id, 'retry')
  const cap = useCapacity(workspaceId, [c.observedState])
  const isService = def.kind === 'service'
  const others = def.versions.supported.filter((v) => v !== def.versions.default)

  const err = install.error instanceof ApiError ? install.error : null
  const fieldError = (f: string) => (err?.field === f ? err.message : undefined)

  const body = (start: boolean) => ({
    version,
    start,
    confirm: true,
    settings: { ...(port !== '' ? { port } : {}), ...(volumeGb !== '' && volume ? { volumeGb } : {}) },
  })
  const run = (start: boolean) => install.mutate(body(start), { onSettled: () => setConfirm(false) })
  const primary = isService ? 'Install and start' : 'Install'

  return (
    <div className="space-y-5 p-5">
      {retry && (
        <div role="alert" className="rounded-xl border border-err-fg/40 bg-err-bg p-3 text-sm text-err-fg">
          <p className="font-semibold">Last install failed</p>
          <p className="mt-0.5 font-mono text-[12px]">{c.message}</p>
        </div>
      )}
      <div>
        <label htmlFor="ver" className="mb-1.5 block text-[13px] font-semibold">Version</label>
        <select id="ver" value={version} onChange={(e) => setVersion(e.target.value)} className="h-10 w-full rounded-[10px] border border-line-strong bg-field px-3 font-mono text-sm">
          {def.versions.supported.map((v) => <option key={v} value={v}>{v}{v === def.versions.default ? '  latest supported' : ''}</option>)}
        </select>
        {others.length > 0 && <p className="mt-1.5 text-xs text-muted">Also available: {others.join(', ')}</p>}
      </div>

      {(defaultPort || volume) && (
        <div className="grid grid-cols-2 gap-3">
          {defaultPort ? <Field label="Port" mono type="number" value={port} onChange={(e) => setPort(e.target.value === '' ? '' : Number(e.target.value))} error={fieldError('port')} /> : <span />}
          {volume && <Field label="Data volume" mono type="number" suffix="GB" value={volumeGb} onChange={(e) => setVolumeGb(e.target.value === '' ? '' : Number(e.target.value))} error={fieldError('volumeGb')} />}
        </div>
      )}

      {def.resources && (
        <div>
          <p className="mb-2 text-[13px] font-semibold">Resource requirements</p>
          <div className="grid grid-cols-3 gap-2">
            <Spec label="vCPU" value={String(def.resources.cpu)} />
            <Spec label="RAM" value={fmtRam(def.resources.ramMB)} />
            <Spec label="Storage" value={`${def.resources.storageGB} GB`} />
          </div>
          {cap.data && <p className="mt-2 text-xs text-muted">Workspace has {cap.data.freeCpu.toFixed(1)} vCPU, {fmtRam(cap.data.freeRamMb)} RAM and {Math.round(cap.data.freeStorageGb)} GB free.</p>}
        </div>
      )}

      {def.install?.requires && def.install.requires.length > 0 && (
        <p className="text-xs text-muted">Requires: {def.install.requires.join(', ')} to be installed in this workspace.</p>
      )}

      {err && !err.field && <p role="alert" className="rounded-lg bg-err-bg p-3 text-sm text-err-fg">{err.message}</p>}
      {err && ['insufficient_resources', 'missing_dependency', 'already_installed'].includes(err.code) && <p role="alert" className="rounded-lg bg-err-bg p-3 text-sm text-err-fg">{err.message}</p>}

      <div>
        <Button variant="primary" className="w-full" onClick={() => setConfirm(true)} loading={install.isPending || retryAction.isPending}>{retry ? 'Retry install' : primary}</Button>
        <p className="mt-2 text-xs text-muted">You will confirm before anything installs. Install and start are tracked separately.</p>
      </div>

      {confirm && (
        <Modal title={`${isService ? 'Install and start' : 'Install'} ${def.name} ${version}?`} onClose={() => setConfirm(false)}
          footer={<>
            <Button onClick={() => setConfirm(false)}>Cancel</Button>
            {isService && <Button onClick={() => run(false)} loading={install.isPending}>Install only</Button>}
            <Button variant="primary" onClick={() => run(isService)} loading={install.isPending}>{primary}</Button>
          </>}>
          <ul className="space-y-1.5 text-sm">
            <li>Version <span className="font-mono text-fg">{version}</span></li>
            {port !== '' && <li>Port <span className="font-mono text-fg">{port}</span></li>}
            {def.resources && <li>Uses {def.resources.cpu} vCPU, {fmtRam(def.resources.ramMB)} RAM and {def.resources.storageGB} GB of this workspace.</li>}
          </ul>
          {install.error && <p role="alert" className="mt-3 rounded-lg bg-err-bg p-2.5 text-err-fg">{install.error.message}</p>}
        </Modal>
      )}
    </div>
  )
}

function OpLog({ ops }: { ops: Operation[] }) {
  const lines = ops.flatMap((o) => [...o.log, ...(o.error ? [`error: ${o.error}`] : [])])
  return (
    <pre aria-label="Operation log" className="max-h-56 overflow-auto rounded-xl bg-night p-3 font-mono text-[11px] leading-relaxed text-night-text">
      {lines.length ? lines.join('\n') : 'No log output yet.'}
    </pre>
  )
}

function InstalledView({ workspaceId, component: c, def }: { workspaceId: string; component: CanvasComponent; def: CatalogComponent }) {
  const start = useComponentAction(workspaceId, c.id, 'start')
  const stop = useComponentAction(workspaceId, c.id, 'stop')
  const remove = useRemoveInstalled(workspaceId, c.id)
  const ops = useOperations(workspaceId)
  const mine = useMemo(() => (ops.data ?? []).filter((o) => o.componentId === c.id).reverse(), [ops.data, c.id])
  const [confirmRemove, setConfirmRemove] = useState(false)
  const [deleteData, setDeleteData] = useState(false)
  const isService = def.install?.strategy === 'image'
  const port = c.settings?.port as number | undefined
  const busy = c.observedState === 'installing'

  return (
    <div className="space-y-5 p-5">
      <dl className="space-y-2 text-sm">
        <div className="flex justify-between"><dt className="text-muted">Version</dt><dd className="font-mono">{c.version}</dd></div>
        {port ? <div className="flex justify-between"><dt className="text-muted">Port</dt><dd className="font-mono">{port}</dd></div> : null}
        {isService && <div className="flex justify-between"><dt className="text-muted">Private host</dt><dd className="font-mono">{def.id}:{port}</dd></div>}
      </dl>
      {busy && (
        <div>
          <div role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={c.progress} aria-label="Install progress" className="h-1.5 overflow-hidden rounded-full bg-neutral-bg">
            <div className="h-full bg-blue transition-[width]" style={{ width: `${c.progress}%` }} />
          </div>
          <p className="mt-1.5 font-mono text-[11px] text-muted">{c.message}</p>
        </div>
      )}
      {c.observedState === 'failed' && <p role="alert" className="rounded-lg bg-err-bg p-3 font-mono text-[12px] text-err-fg">{c.message}</p>}

      <div className="flex flex-wrap gap-2">
        {isService && c.observedState === 'stopped' && <Button variant="primary" onClick={() => start.mutate()} loading={start.isPending}>Start</Button>}
        {isService && c.observedState === 'running' && <Button onClick={() => stop.mutate()} loading={stop.isPending}>Stop</Button>}
        <Button variant="danger" disabled={busy} onClick={() => setConfirmRemove(true)}>Remove…</Button>
      </div>
      {(start.error || stop.error) && <p role="alert" className="text-sm text-err-fg">{(start.error ?? stop.error)!.message}</p>}

      <div>
        <p className="mb-2 text-[13px] font-semibold">Logs</p>
        <OpLog ops={mine} />
      </div>

      {confirmRemove && (
        <Modal title={`Remove ${def.name}?`} onClose={() => setConfirmRemove(false)}
          footer={<>
            <Button onClick={() => setConfirmRemove(false)}>Cancel</Button>
            <Button variant="danger" loading={remove.isPending} onClick={() => remove.mutate(deleteData, { onSuccess: () => setConfirmRemove(false) })}>Remove {def.name}</Button>
          </>}>
          <p>The card is removed from the canvas{isService ? ' and the service is stopped and deleted' : ''}.</p>
          {isService && (
            <label className="mt-3 flex items-center gap-2 text-sm text-fg">
              <input type="checkbox" checked={deleteData} onChange={(e) => setDeleteData(e.target.checked)} className="accent-blue" />
              Also delete its data volume (cannot be undone)
            </label>
          )}
          {remove.error && <p role="alert" className="mt-3 text-err-fg">{remove.error.message}</p>}
        </Modal>
      )}
    </div>
  )
}
