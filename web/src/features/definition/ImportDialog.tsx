import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { ApiError } from '@/api/client'
import { Button } from '@/components/ui/Button'
import { Field } from '@/components/ui/Field'
import { Modal } from '@/components/ui/Modal'
import { groupBy } from '@/lib/group'
import { useImportDefinition, type ImportResult } from './hooks'

const TODO_LABEL: Record<string, string> = { credential: 'Connection credentials', secret_variable: 'Secret variables', agent_key: 'Code agent keys', confirm_install: 'Installs to confirm' }

/** Create a workspace from a definition file: check first, then import. */
export function ImportDefinitionDialog({ onClose }: { onClose: () => void }) {
  const nav = useNavigate()
  const imp = useImportDefinition()
  const [text, setText] = useState('')
  const [name, setName] = useState('')
  const [autoInstall, setAutoInstall] = useState(false)
  const [checked, setChecked] = useState<ImportResult | null>(null)
  const [created, setCreated] = useState<ImportResult | null>(null)
  const err = imp.error instanceof ApiError ? imp.error : null

  const run = (dryRun: boolean) =>
    imp.mutate({ definition: text, name, autoInstall, dryRun }, { onSuccess: (r) => (dryRun ? setChecked(r) : setCreated(r)) })
  const invalidate = () => { setChecked(null); imp.reset() }

  if (created?.workspace) {
    const groups = groupBy(created.todos, (t) => t.kind)
    return (
      <Modal title="Workspace is being created" onClose={onClose} width={560}
        footer={<Button variant="primary" onClick={() => nav(`/w/${created.workspace!.id}/provisioning`)}>Watch it provision</Button>}>
        <p className="text-fg">“{created.workspace.name}” is provisioning. The components are added as soon as it is ready.</p>
        {created.todos.length > 0 && (
          <div className="mt-4 text-fg">
            <p className="font-semibold">Still needed from you</p>
            <p className="text-xs text-muted">Secrets are never part of a definition, so these are entered again.</p>
            {groups.map(([k, items]) => (
              <div key={k} className="mt-3">
                <p className="text-[11px] font-bold uppercase tracking-wider text-faint">{TODO_LABEL[k] ?? k}</p>
                <ul className="mt-1 space-y-1 text-[13px]">{items.map((t, i) => <li key={i}><span className="font-semibold">{t.subject}</span> <span className="text-muted">· {t.detail}</span></li>)}</ul>
              </div>
            ))}
          </div>
        )}
      </Modal>
    )
  }

  return (
    <Modal title="Import a workspace definition" onClose={onClose} width={620}
      footer={<>
        <Button onClick={onClose}>Cancel</Button>
        <Button disabled={!text.trim()} loading={imp.isPending && imp.variables?.dryRun} onClick={() => run(true)}>Check</Button>
        <Button variant="primary" disabled={!checked} loading={imp.isPending && imp.variables?.dryRun === false} onClick={() => run(false)}>Create workspace</Button>
      </>}>
      <div className="space-y-4 text-fg">
        <div>
          <label htmlFor="def-text" className="mb-1.5 block text-[13px] font-semibold">Definition file</label>
          <textarea id="def-text" rows={8} value={text} spellCheck={false} onChange={(e) => { setText(e.target.value); invalidate() }}
            className="w-full rounded-[10px] border border-line-strong bg-field p-3 font-mono text-[12px]" placeholder="Paste a definition (YAML or JSON), or choose a file" />
          <label className="mt-2 inline-block text-xs text-muted">or choose a file <input type="file" accept=".yaml,.yml,.json" aria-label="Choose a definition file" className="ml-1 text-xs"
            onChange={async (e) => { const f = e.target.files?.[0]; if (f) { setText(await f.text()); invalidate() } }} /></label>
        </div>
        <Field label="Workspace name" optional mono value={name} onChange={(e) => { setName(e.target.value.toLowerCase()); invalidate() }}
          hint="Leave blank to use the name in the file." error={err?.field === 'name' ? err.message : undefined} />
        <label className="flex items-start gap-2 text-sm">
          <input type="checkbox" className="mt-1" checked={autoInstall} onChange={(e) => { setAutoInstall(e.target.checked); invalidate() }} />
          <span>Install the services and runtimes automatically<span className="block text-xs text-muted">Otherwise they are added as drafts for you to confirm one by one.</span></span>
        </label>
        {err && err.code === 'invalid_definition' && (
          <div role="alert" className="rounded-lg bg-err-bg p-3 text-sm text-err-fg">
            <p className="font-semibold">This definition can't be imported yet</p>
            <ul className="mt-1 list-disc space-y-0.5 pl-5">{(err.issues ?? []).slice(0, 8).map((i, k) => <li key={k}><span className="font-mono text-[12px]">{i.path}</span>: {i.message}</li>)}</ul>
          </div>
        )}
        {err && err.code !== 'invalid_definition' && err.field !== 'name' && <p role="alert" className="rounded-lg bg-err-bg p-3 text-sm text-err-fg">{err.message}</p>}
        {checked && (
          <div role="status" className="rounded-lg border border-line bg-raised p-3 text-sm">
            <p className="font-semibold text-ok-fg"><span aria-hidden>✓ </span>The definition is valid. Ready to create the workspace.</p>
            {checked.todos.length > 0 && <p className="mt-1 text-muted">You'll need to supply {checked.todos.length} {checked.todos.length === 1 ? 'item' : 'items'} afterwards (credentials, secrets, confirmations).</p>}
          </div>
        )}
      </div>
    </Modal>
  )
}
