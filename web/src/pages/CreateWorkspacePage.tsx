import clsx from 'clsx'
import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { ApiError } from '@/api/client'
import { useCreateWorkspace, useHost, useNameAvailable } from '@/api/hooks'
import { Button } from '@/components/ui/Button'
import { Field } from '@/components/ui/Field'
import { TopBar } from '@/layouts/TopBar'
import { ResourceRow } from '@/features/workspaces/ResourceRow'
import { MIN, validateResources } from '@/lib/resources'
import { AdvancedSetup, type AdvancedState } from '@/features/agent/AdvancedSetup'

const NAME_RE = /^[a-z0-9][a-z0-9-]{0,46}[a-z0-9]$/

function useDebounced<T>(v: T, ms = 300) {
  const [d, setD] = useState(v)
  useEffect(() => { const t = setTimeout(() => setD(v), ms); return () => clearTimeout(t) }, [v, ms])
  return d
}

function Section({ n, title, aside, children }: { n: string; title: string; aside?: React.ReactNode; children: React.ReactNode }) {
  return (
    <section className="rounded-card border border-line bg-panel p-6">
      <div className="mb-4 flex items-baseline justify-between">
        <h2 className="flex items-baseline gap-3 text-base font-bold"><span className="font-mono text-[11px] text-link">{n}</span>{title}</h2>
        {aside}
      </div>
      {children}
    </section>
  )
}

export default function CreateWorkspacePage() {
  const nav = useNavigate()
  const { data: host } = useHost()
  const create = useCreateWorkspace()
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [ramGb, setRam] = useState(8)
  const [cpu, setCpu] = useState(4)
  const [storageGb, setStorage] = useState(40)
  const [gpu, setGpu] = useState(false)
  const [agent, setAgent] = useState<AdvancedState>({ started: false, tested: false, input: null })
  const debouncedName = useDebounced(name)
  const avail = useNameAvailable(NAME_RE.test(debouncedName) ? debouncedName : '')

  const nameError = name && !NAME_RE.test(name) ? 'Use 2 to 48 lowercase letters, digits or hyphens, starting and ending with a letter or digit.' : undefined
  const nameTaken = !nameError && debouncedName === name && avail.data?.available === false
  const limitErr = useMemo(() => (host ? validateResources({ cpu, ramGb, storageGb, gpu }, host) : null), [host, cpu, ramGb, storageGb, gpu])

  const serverErr = create.error instanceof ApiError ? create.error : null
  const fieldErr = (f: string) => (limitErr?.field === f ? limitErr.message : serverErr?.field === f ? serverErr.message : undefined)
  // An untouched agent section never blocks creation; a started but untested one does (PRD 6.4).
  const agentBlocked = agent.started && !agent.tested
  const canCreate = !!host && !!name && !nameError && !nameTaken && !limitErr && !agentBlocked

  const submit = () => {
    if (!canCreate) return
    create.mutate({ name, description, cpu, ramGb, storageGb, gpu, ...(agent.input ? { agent: agent.input } : {}) }, { onSuccess: (w) => nav(`/w/${w.id}/provisioning`) })
  }

  return (
    <div className="theme-light flex min-h-full flex-col">
      <TopBar crumbs={[{ label: 'Workspaces', to: '/workspaces' }, { label: 'New code space' }]} />
      <main className="mx-auto grid w-full max-w-[1200px] flex-1 gap-8 px-8 py-10 lg:grid-cols-[1fr_340px]">
        <form className="space-y-5" onSubmit={(e) => { e.preventDefault(); submit() }}>
          <div>
            <h1 className="text-[34px] font-extrabold tracking-tight">Create Code Space</h1>
            <p className="mt-2 max-w-xl text-muted">Configure the environment for your workspace. Everything except the operating system can be changed later in Workspace Settings.</p>
          </div>

          <Section n="01" title="Name and description">
            <div className="grid gap-4 sm:grid-cols-2">
              <div>
                <Field label="Workspace name" value={name} onChange={(e) => setName(e.target.value.toLowerCase())} placeholder="payments-service" autoComplete="off" error={nameError ?? (nameTaken ? 'A workspace with this name already exists.' : fieldErr('name'))} />
                {!nameError && !nameTaken && debouncedName === name && avail.data?.available && <p className="mt-1.5 text-xs font-medium text-ok-fg">Available</p>}
              </div>
              <Field label="Description" optional value={description} onChange={(e) => setDescription(e.target.value)} placeholder="What is this workspace for?" />
            </div>
          </Section>

          <Section n="02" title="Operating system">
            <div className="flex items-center gap-4 rounded-xl border border-line bg-bg p-3.5">
              <span className="grid size-10 place-items-center rounded-lg border border-line bg-panel font-mono text-xs font-bold text-ubuntu">Ub</span>
              <div className="flex-1">
                <p className="font-semibold">Ubuntu 24.04 LTS</p>
                <p className="font-mono text-[11px] text-muted">noble · x86_64 · default</p>
              </div>
              <span className="rounded-md bg-neutral-bg px-2.5 py-1 text-xs font-medium text-muted"><span aria-hidden>🔒 </span>Fixed after creation</span>
            </div>
            <p className="mt-3 text-sm text-muted">The operating system cannot be changed later. Changing it requires creating a new workspace.</p>
          </Section>

          <Section n="03" title="Resources" aside={host && <span className="text-xs text-faint">Per-workspace limit: {host.maxCpu} vCPU · {host.maxRamGb} GB RAM · {host.maxStorageGb} GB</span>}>
            <div className="space-y-4">
              <ResourceRow label="RAM" unit="GB" value={ramGb} min={MIN.ramGb} max={host?.maxRamGb ?? 24} onChange={setRam} error={fieldErr('ramGb')} />
              <ResourceRow label="CPU" unit="vCPU" value={cpu} min={MIN.cpu} max={host?.maxCpu ?? 12} onChange={setCpu} error={fieldErr('cpu')} />
              <ResourceRow label="Storage" unit="GB" value={storageGb} min={MIN.storageGb} max={host?.maxStorageGb ?? 200} onChange={setStorage} error={fieldErr('storageGb')} />
            </div>
            <div className={clsx('mt-5 flex items-center gap-3 rounded-xl border border-dashed border-line-strong p-3.5', !host?.gpuAvailable && 'opacity-80')}>
              <input id="gpu" type="checkbox" role="switch" checked={gpu} disabled={!host?.gpuAvailable} onChange={(e) => setGpu(e.target.checked)} className="size-4 accent-blue" />
              <label htmlFor="gpu" className="text-sm">
                <span className="font-semibold">GPU access</span>
                <span className="block text-xs text-muted">
                  {host?.gpuAvailable ? 'Attach the host GPU to this workspace.' : `Unavailable: ${host?.gpuReason ?? 'this execution backend has no GPU drivers installed.'}`}
                </span>
              </label>
            </div>
          </Section>

          <AdvancedSetup onChange={setAgent} />
        </form>

        <aside className="lg:sticky lg:top-8 lg:self-start">
          <div className="rounded-card border border-line bg-panel p-6">
            <h2 className="text-base font-bold">Summary</h2>
            <dl className="mt-4 space-y-2.5 text-sm">
              {[['OS', 'Ubuntu 24.04 LTS'], ['CPU', `${Number.isFinite(cpu) ? cpu : '–'} vCPU`], ['RAM', `${Number.isFinite(ramGb) ? ramGb : '–'} GB`], ['Storage', `${Number.isFinite(storageGb) ? storageGb : '–'} GB`], ['GPU', gpu ? 'Attached' : 'None'], ['Code agent', !agent.started ? 'Not configured' : `${agent.providerName ?? 'Custom'} · ${agent.tested ? 'tested' : 'untested'}`]].map(([k, v]) => (
                <div key={k} className="flex justify-between"><dt className="text-muted">{k}</dt><dd className="font-mono text-[13px]">{v}</dd></div>
              ))}
            </dl>
            {agentBlocked && (
              <p role="alert" className="mt-4 rounded-lg bg-warn-bg p-3 text-[13px] text-warn-fg">The code agent section is started but not tested. Test Configuration, or clear the section.</p>
            )}
            {(limitErr || serverErr) && (
              <p role="alert" className="mt-4 rounded-lg bg-err-bg p-3 text-[13px] text-err-fg">
                <strong>Can't create this combination.</strong> {limitErr?.message ?? serverErr?.message}
              </p>
            )}
            <Button variant="primary" size="lg" className="mt-5 w-full" disabled={!canCreate} loading={create.isPending} onClick={submit}>Create Code Space</Button>
            <p className="mt-4 text-xs leading-relaxed text-muted">An unconfigured agent never blocks creation. Only an agent section you started and left untested does.</p>
          </div>
          <p className="mt-4 px-1 font-mono text-[11px] leading-relaxed text-faint">
            Next: Allocating → Preparing environment → Starting → Ready. The canvas opens when the workspace is Ready.
          </p>
        </aside>
      </main>
    </div>
  )
}
