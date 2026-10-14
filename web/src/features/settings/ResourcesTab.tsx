import { useEffect, useMemo, useState } from 'react'
import { ApiError } from '@/api/client'
import { useHost } from '@/api/hooks'
import type { SettingsPreview, Workspace } from '@/api/types'
import { Button } from '@/components/ui/Button'
import { Modal } from '@/components/ui/Modal'
import { ResourceRow } from '@/features/workspaces/ResourceRow'
import { MIN } from '@/lib/resources'
import { WorkspaceUsage } from '@/features/metrics/CardMetrics'
import { useApplySettings, usePreviewSettings, type SettingsBody } from './hooks'

const LABEL: Record<string, string> = { cpu: 'CPU', ramGb: 'RAM', storageGb: 'Storage', gpu: 'GPU', name: 'Name', description: 'Description' }
const UNIT: Record<string, string> = { cpu: ' vCPU', ramGb: ' GB', storageGb: ' GB' }
const show = (f: string, v: unknown) => (f === 'gpu' ? (v ? 'On' : 'Off') : `${v}${UNIT[f] ?? ''}`)

export function ResourcesTab({ workspace: w }: { workspace: Workspace }) {
  const { data: host } = useHost()
  const [cpu, setCpu] = useState(w.cpu)
  const [ramGb, setRam] = useState(w.ramGb)
  const [storageGb, setStorage] = useState(w.storageGb)
  const [gpu, setGpu] = useState(w.gpu)
  const preview = usePreviewSettings(w.id)
  const apply = useApplySettings(w.id)
  const [confirm, setConfirm] = useState(false)

  // Re-sync when the workspace changes underneath (after an apply or restart).
  useEffect(() => { setCpu(w.cpu); setRam(w.ramGb); setStorage(w.storageGb); setGpu(w.gpu) }, [w.cpu, w.ramGb, w.storageGb, w.gpu])

  const body = useMemo<SettingsBody>(() => {
    const b: SettingsBody = {}
    if (cpu !== w.cpu) b.cpu = cpu
    if (ramGb !== w.ramGb) b.ramGb = ramGb
    if (storageGb !== w.storageGb) b.storageGb = storageGb
    if (gpu !== w.gpu) b.gpu = gpu
    return b
  }, [cpu, ramGb, storageGb, gpu, w])
  const dirty = Object.keys(body).length > 0

  // Ask the server what would change; it also validates against host limits.
  const previewMutate = preview.mutate
  useEffect(() => {
    if (!dirty || [cpu, ramGb, storageGb].some((n) => !Number.isFinite(n))) return
    const t = setTimeout(() => previewMutate(body), 350)
    return () => clearTimeout(t)
  }, [body, dirty, cpu, ramGb, storageGb, previewMutate])

  const err = preview.error instanceof ApiError ? preview.error : apply.error instanceof ApiError ? apply.error : null
  const fieldErr = (f: string) => (err?.field === f ? err.message : undefined)
  const pv: SettingsPreview | undefined = dirty ? preview.data : undefined
  const canApply = dirty && !!pv && !err && !apply.isPending
  const stopped = w.status === 'stopped'

  const doApply = () => apply.mutate({ ...body, ...(pv?.restartRequired ? { confirmRestart: true } : {}) }, { onSuccess: () => setConfirm(false) })

  return (
    <section aria-label="Resource settings" className="max-w-3xl space-y-6 pb-28">
      <div>
        <h2 className="text-2xl font-extrabold tracking-tight">Resources</h2>
        <p className="mt-1 text-muted">CPU and RAM apply immediately. Storage and GPU changes restart the workspace. {host && <span className="text-faint">Host limit: {host.maxCpu} vCPU · {host.maxRamGb} GB RAM · {host.maxStorageGb} GB.</span>}</p>
      </div>
      {w.status === 'ready' && <WorkspaceUsage workspaceId={w.id} limits={{ cpu: w.cpu, ramGb: w.ramGb, storageGb: w.storageGb }} />}
      <div className="space-y-5 rounded-card border border-line bg-panel p-6">
        <ResourceRow label="RAM" unit="GB" value={ramGb} min={MIN.ramGb} max={host?.maxRamGb ?? 24} onChange={setRam} error={fieldErr('ramGb')} />
        <ResourceRow label="CPU" unit="vCPU" value={cpu} min={MIN.cpu} max={host?.maxCpu ?? 12} onChange={setCpu} error={fieldErr('cpu')} />
        <ResourceRow label="Storage" unit="GB" value={storageGb} min={MIN.storageGb} max={host?.maxStorageGb ?? 200} onChange={setStorage} error={fieldErr('storageGb')} />
        <p className="pl-[88px] text-xs text-muted">Storage can be increased. Reducing it is rejected if the backend cannot shrink volumes or if it would fall below what installed components use.</p>
        <div className="flex items-center gap-3 rounded-xl border border-dashed border-line-strong p-3.5">
          <input id="gpu" type="checkbox" role="switch" checked={gpu} disabled={!host?.gpuAvailable && !w.gpu} onChange={(e) => setGpu(e.target.checked)} className="size-4 accent-blue" />
          <label htmlFor="gpu" className="text-sm">
            <span className="font-semibold">GPU access</span>
            <span className="block text-xs text-muted">{host?.gpuAvailable || w.gpu ? 'Attach the host GPU to this workspace. Requires a restart.' : `Unavailable: ${host?.gpuReason ?? 'this execution backend has no GPU drivers installed.'}`}</span>
          </label>
        </div>
        {fieldErr('gpu') && <p role="alert" className="text-xs text-err-fg">{fieldErr('gpu')}</p>}
      </div>

      {w.restartRequired && <p role="status" className="rounded-lg bg-info-bg p-3 text-sm text-info-fg">A restart is applying your changes…</p>}

      {dirty && (
        <div role="region" aria-label="Review changes" className="fixed inset-x-0 bottom-0 z-30 border-t border-line-strong bg-panel/95 px-8 py-4 backdrop-blur md:left-[260px]">
          <div className="mx-auto flex max-w-3xl flex-wrap items-center gap-4">
            <div className="min-w-0 flex-1">
              <p className="text-sm font-semibold">Review changes</p>
              {err ? (
                <p role="alert" className="text-sm text-err-fg">{err.message}</p>
              ) : pv ? (
                <ul className="mt-1 space-y-0.5 text-[13px]">
                  {pv.changes.map((c) => (
                    <li key={c.field}><span className="font-medium">{LABEL[c.field] ?? c.field}</span> <span className="font-mono text-muted">{show(c.field, c.from)} → {show(c.field, c.to)}</span> <span className={c.appliesImmediately ? 'text-ok-fg' : 'text-warn-fg'}>{c.appliesImmediately ? 'applies immediately' : 'needs restart'}</span></li>
                  ))}
                </ul>
              ) : <p className="text-sm text-muted">Checking against host limits…</p>}
            </div>
            <Button onClick={() => { setCpu(w.cpu); setRam(w.ramGb); setStorage(w.storageGb); setGpu(w.gpu) }}>Discard</Button>
            <Button variant="primary" disabled={!canApply} loading={apply.isPending} onClick={() => (pv?.restartRequired ? setConfirm(true) : doApply())}>{pv?.restartRequired ? 'Apply and restart' : 'Apply changes'}</Button>
          </div>
        </div>
      )}

      {confirm && (
        <Modal title="Restart workspace?" onClose={() => setConfirm(false)}
          footer={<><Button onClick={() => setConfirm(false)}>Cancel</Button><Button variant="primary" loading={apply.isPending} onClick={doApply}>Restart and apply</Button></>}>
          <p>The workspace container is replaced so these changes take effect. Files, installed services and their data are kept. {stopped ? 'It will stay stopped afterwards.' : 'Running terminals and processes inside the workspace stop.'}</p>
          <ul className="mt-3 space-y-1 font-mono text-xs">{pv?.changes.map((c) => <li key={c.field}>{LABEL[c.field]}: {show(c.field, c.from)} → {show(c.field, c.to)}</li>)}</ul>
          {apply.error && <p role="alert" className="mt-3 text-err-fg">{apply.error.message}</p>}
        </Modal>
      )}
    </section>
  )
}
