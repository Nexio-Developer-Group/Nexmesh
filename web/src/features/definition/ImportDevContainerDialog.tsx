import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { ApiError } from '@/api/client'
import { Button } from '@/components/ui/Button'
import { Field } from '@/components/ui/Field'
import { Modal } from '@/components/ui/Modal'
import { useImportDevContainer, type DevContainerImport } from './hooks'
import { ReportList } from './ReportList'

/** Create a workspace from a devcontainer.json, showing exactly what was applied, adapted or ignored. */
export function ImportDevContainerDialog({ onClose }: { onClose: () => void }) {
  const nav = useNavigate()
  const imp = useImportDevContainer()
  const [text, setText] = useState('')
  const [name, setName] = useState('')
  const [autoInstall, setAutoInstall] = useState(true)
  const [runPostCreate, setRun] = useState(false)
  const [checked, setChecked] = useState<DevContainerImport | null>(null)
  const [created, setCreated] = useState<DevContainerImport | null>(null)
  const err = imp.error instanceof ApiError ? imp.error : null
  const reset = () => { setChecked(null); imp.reset() }
  const run = (dryRun: boolean) => imp.mutate({ content: text, name, autoInstall, runPostCreate, dryRun }, { onSuccess: (r) => (dryRun ? setChecked(r) : setCreated(r)) })

  if (created?.workspace) {
    return (
      <Modal title="Workspace is being created" width={620} onClose={onClose} footer={<Button variant="primary" onClick={() => nav(`/w/${created.workspace!.id}/provisioning`)}>Watch it provision</Button>}>
        <p className="text-fg">“{created.workspace.name}” is provisioning from your dev container file.{created.postCreate.length > 0 && runPostCreate ? ' Post-create commands run once it is ready.' : ''}</p>
        <div className="mt-4 max-h-[50vh] overflow-y-auto text-fg"><ReportList report={created.report} /></div>
      </Modal>
    )
  }

  const report = checked?.report
  return (
    <Modal title="Import a dev container" width={700} onClose={onClose}
      footer={<>
        <Button onClick={onClose}>Cancel</Button>
        <Button disabled={!text.trim()} loading={imp.isPending && imp.variables?.dryRun} onClick={() => run(true)}>Check</Button>
        <Button variant="primary" disabled={!checked} loading={imp.isPending && imp.variables?.dryRun === false} onClick={() => run(false)}>Create workspace</Button>
      </>}>
      <div className="space-y-4 text-fg">
        <div>
          <label htmlFor="dc-text" className="mb-1.5 block text-[13px] font-semibold">devcontainer.json</label>
          <textarea id="dc-text" rows={8} value={text} spellCheck={false} onChange={(e) => { setText(e.target.value); reset() }}
            className="w-full rounded-[10px] border border-line-strong bg-field p-3 font-mono text-[12px]" placeholder={'{\n  "name": "My project",\n  "forwardPorts": [3000]\n}'} />
          <label className="mt-2 inline-block text-xs text-muted">or choose a file <input type="file" accept=".json,.jsonc" aria-label="Choose a devcontainer.json file" className="ml-1 text-xs"
            onChange={async (e) => { const f = e.target.files?.[0]; if (f) { setText(await f.text()); reset() } }} /></label>
        </div>
        <Field label="Workspace name" optional mono value={name} onChange={(e) => { setName(e.target.value.toLowerCase()); reset() }} hint="Leave blank to use the name from the file." error={err?.field === 'name' ? err.message : undefined} />
        <label className="flex items-start gap-2 text-sm"><input type="checkbox" className="mt-1" checked={autoInstall} onChange={(e) => { setAutoInstall(e.target.checked); reset() }} />
          <span>Install the runtimes the file asks for<span className="block text-xs text-muted">Node.js, Python and Go features become installed runtimes.</span></span></label>
        <label className="flex items-start gap-2 text-sm"><input type="checkbox" className="mt-1" checked={runPostCreate} onChange={(e) => { setRun(e.target.checked); reset() }} />
          <span>Run the post-create commands once after creation<span className="block text-xs text-muted">Only enable this for files you trust: the commands run in your new workspace.</span></span></label>
        {err && err.field !== 'name' && <p role="alert" className="rounded-lg bg-err-bg p-3 text-sm text-err-fg">{err.code === 'invalid_definition' && err.issues?.length ? `${err.message} (${err.issues[0].path})` : err.message}</p>}
        {checked && report && (
          <div role="status" className="space-y-3">
            <p className="rounded-lg border border-line bg-raised p-3 text-sm"><span className="font-semibold text-ok-fg"><span aria-hidden>✓ </span>Ready to create “{checked.name}”</span> with {checked.resources.cpu} vCPU, {checked.resources.ramGb} GB RAM and {checked.resources.storageGb} GB storage on Ubuntu 24.04 LTS.</p>
            {runPostCreate && checked.postCreate.length > 0 && (
              <div className="rounded-lg border border-line p-3 text-[13px]"><p className="font-semibold">Commands that will run after creation</p>
                <ul className="mt-1 space-y-0.5 font-mono text-[12px]">{checked.postCreate.map((c, i) => <li key={i}>$ {c}</li>)}</ul></div>
            )}
            <div className="max-h-[40vh] overflow-y-auto"><ReportList report={report} /></div>
          </div>
        )}
      </div>
    </Modal>
  )
}
