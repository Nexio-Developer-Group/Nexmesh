import { useMemo, useState } from 'react'
import { ApiError } from '@/api/client'
import { Button } from '@/components/ui/Button'
import { Field } from '@/components/ui/Field'
import { Modal } from '@/components/ui/Modal'
import { useComponents } from '@/features/canvas/hooks'
import { useDeleteVariable, useImportVariables, useSaveVariable, useVariables, type VarRow } from './hooks'

type Editing = { row?: VarRow } | null

export function VariablesTab({ workspaceId }: { workspaceId: string }) {
  const { data, isPending, error } = useVariables(workspaceId)
  const comps = useComponents(workspaceId)
  const del = useDeleteVariable(workspaceId)
  const [editing, setEditing] = useState<Editing>(null)
  const [importing, setImporting] = useState(false)
  const [confirmDel, setConfirmDel] = useState<VarRow | null>(null)
  const names = useMemo(() => new Map((comps.data ?? []).map((c) => [c.id, c.connName || c.catalogId])), [comps.data])

  return (
    <section aria-label="Variables" className="max-w-4xl space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-2xl font-extrabold tracking-tight">Variables</h2>
          <p className="mt-1 max-w-xl text-muted">
            Environment variables for every terminal and command in this workspace. Secret values are encrypted and can never be read back.
            Changes apply to <strong className="text-fg">new</strong> processes; restart open terminals to pick them up.
          </p>
        </div>
        <div className="flex gap-2">
          <a className="inline-flex h-8 items-center rounded-[10px] border border-line-strong bg-raised px-3 text-xs font-semibold hover:border-blue" href={`/api/workspaces/${workspaceId}/variables/export`} download=".env.example">Export names</a>
          <Button size="sm" onClick={() => setImporting(true)}>Import .env</Button>
          <Button size="sm" variant="primary" onClick={() => setEditing({})}>Add variable</Button>
        </div>
      </div>

      {error && <p role="alert" className="rounded-lg bg-err-bg p-3 text-sm text-err-fg">{error.message}</p>}
      {isPending && <p className="text-muted">Loading…</p>}
      {data && (
        <div className="overflow-hidden rounded-card border border-line bg-panel">
          <table className="w-full text-left text-[13px]">
            <caption className="sr-only">Environment variables</caption>
            <thead className="border-b border-line text-[11px] uppercase tracking-wider text-faint">
              <tr><th scope="col" className="px-4 py-2.5">Name</th><th scope="col" className="px-4 py-2.5">Value</th><th scope="col" className="px-4 py-2.5">Scope</th><th scope="col" className="px-4 py-2.5">Environment</th><th scope="col" className="px-4 py-2.5"><span className="sr-only">Actions</span></th></tr>
            </thead>
            <tbody>
              {data.variables.length === 0 && data.generated.length === 0 && (
                <tr><td colSpan={5} className="px-4 py-8 text-center text-muted">No variables yet. Add one, or import a .env file.</td></tr>
              )}
              {data.variables.map((v) => (
                <tr key={v.name + '|' + (v.componentId ?? '') + '|' + (v.environment ?? '')} className="border-b border-line last:border-0">
                  <td className="px-4 py-3">
                    <span className="font-mono text-[12.5px] font-semibold">{v.name}</span>
                    {v.overridesGenerated && <span className="ml-2 rounded bg-warn-bg px-1.5 py-0.5 text-[10.5px] font-semibold text-warn-fg">Overrides generated</span>}
                    {v.description && <p className="mt-0.5 text-xs text-muted">{v.description}</p>}
                  </td>
                  <td className="px-4 py-3 font-mono text-[12.5px]">
                    {v.secret ? <span><span aria-hidden>••••••••</span> <span className="ml-1 rounded bg-neutral-bg px-1.5 py-0.5 font-sans text-[10.5px] font-semibold text-muted">Secret</span></span> : v.value || <span className="text-faint">empty</span>}
                  </td>
                  <td className="px-4 py-3 text-muted">{v.componentId ? names.get(v.componentId) ?? 'Component' : 'Workspace'}</td>
                  <td className="px-4 py-3">{v.environment
                    ? <span className={v.environment === 'production' ? 'font-semibold text-warn-fg' : 'text-fg'}>{v.environment === 'production' && <span aria-hidden>▲ </span>}{v.environment}</span>
                    : <span className="text-muted">All</span>}</td>
                  <td className="px-4 py-3 text-right">
                    <Button size="sm" variant="ghost" aria-label={`Edit ${v.name}`} onClick={() => setEditing({ row: v })}>Edit</Button>
                    <Button size="sm" variant="ghost" aria-label={`Delete ${v.name}`} onClick={() => setConfirmDel(v)}>Delete</Button>
                  </td>
                </tr>
              ))}
              {data.generated.map((g) => (
                <tr key={'g|' + g.name} className="border-b border-line bg-raised/40 last:border-0">
                  <td className="px-4 py-3 font-mono text-[12.5px] text-muted"><span aria-hidden>🔒 </span>{g.name}</td>
                  <td className="px-4 py-3 text-muted">{g.overridden ? 'Overridden by a variable above' : 'Generated from the connection'}</td>
                  <td className="px-4 py-3 text-muted">From {g.componentName || 'connection'}</td>
                  <td className="px-4 py-3 text-muted">Active</td>
                  <td className="px-4 py-3 text-right text-xs text-faint">Read-only</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {editing && <VariableDialog workspaceId={workspaceId} row={editing.row} components={comps.data ?? []} onClose={() => setEditing(null)} />}
      {importing && <ImportDialog workspaceId={workspaceId} onClose={() => setImporting(false)} />}
      {confirmDel && (
        <Modal title={`Delete ${confirmDel.name}?`} onClose={() => setConfirmDel(null)}
          footer={<><Button onClick={() => setConfirmDel(null)}>Cancel</Button>
            <Button variant="danger" loading={del.isPending} onClick={() => del.mutate({ name: confirmDel.name, componentId: confirmDel.componentId, environment: confirmDel.environment }, { onSuccess: () => setConfirmDel(null) })}>Delete variable</Button></>}>
          Processes already running keep their copy; new ones will no longer see it.
        </Modal>
      )}
    </section>
  )
}

function VariableDialog({ workspaceId, row, components, onClose }: { workspaceId: string; row?: VarRow; components: { id: string; connName?: string; catalogId: string }[]; onClose: () => void }) {
  const save = useSaveVariable(workspaceId)
  const [name, setName] = useState(row?.name ?? '')
  const [secret, setSecret] = useState(row?.secret ?? false)
  const [value, setValue] = useState(row && !row.secret ? row.value ?? '' : '')
  const [description, setDescription] = useState(row?.description ?? '')
  const [componentId, setComponentId] = useState(row?.componentId ?? '')
  const [environment, setEnvironment] = useState(row?.environment ?? '')
  const editing = !!row
  const err = save.error instanceof ApiError ? save.error : null
  // A secret's stored value is write-only: leaving the box empty keeps it.
  const keepsValue = editing && row.secret && secret && value === ''
  const canSave = !!name && (keepsValue || value !== '')

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    save.mutate({ name, componentId, environment, secret, description, ...(keepsValue ? {} : { value }) }, { onSuccess: onClose })
  }

  return (
    <Modal title={editing ? `Edit ${row.name}` : 'Add variable'} onClose={onClose} width={520}
      footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" type="submit" form="var-form" disabled={!canSave} loading={save.isPending}>Save variable</Button></>}>
      <form id="var-form" onSubmit={submit} className="space-y-4 text-fg">
        <Field label="Name" mono value={name} disabled={editing} onChange={(e) => setName(e.target.value)} placeholder="STRIPE_API_KEY"
          error={err?.field === 'name' ? err.message : undefined} />
        <Field label="Value" mono type={secret ? 'password' : 'text'} autoComplete="off" value={value} onChange={(e) => setValue(e.target.value)}
          placeholder={keepsValue ? 'Leave blank to keep the current value' : ''}
          hint={secret ? 'Stored encrypted. It will not be shown again.' : undefined}
          error={err?.field === 'value' ? err.message : undefined} />
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={secret} onChange={(e) => setSecret(e.target.checked)} /> Secret (hide the value after saving)
        </label>
        <div>
          <label htmlFor="var-scope" className="mb-1.5 block text-[13px] font-semibold">Scope</label>
          <select id="var-scope" value={componentId} disabled={editing} onChange={(e) => setComponentId(e.target.value)}
            className="h-10 w-full rounded-[10px] border border-line-strong bg-field px-3 text-sm">
            <option value="">Whole workspace</option>
            {components.map((c) => <option key={c.id} value={c.id}>{c.connName || c.catalogId}</option>)}
          </select>
          <p className="mt-1.5 text-xs text-muted">Component-scoped values win over workspace values with the same name.</p>
        </div>
        <div>
          <label htmlFor="var-env" className="mb-1.5 block text-[13px] font-semibold">Environment</label>
          <select id="var-env" value={environment} disabled={editing} onChange={(e) => setEnvironment(e.target.value)}
            className="h-10 w-full rounded-[10px] border border-line-strong bg-field px-3 text-sm">
            <option value="">All environments</option>
            <option value="development">Development only</option>
            <option value="staging">Staging only</option>
            <option value="production">Production only</option>
          </select>
          <p className="mt-1.5 text-xs text-muted">A variable for one environment is injected only while that environment is active, and overrides an all-environments variable with the same name.</p>
        </div>
        <Field label="Description" optional value={description} onChange={(e) => setDescription(e.target.value)} error={err?.field === 'description' ? err.message : undefined} />
        {err && !err.field && <p role="alert" className="rounded-lg bg-err-bg p-3 text-sm text-err-fg">{err.message}</p>}
      </form>
    </Modal>
  )
}

function ImportDialog({ workspaceId, onClose }: { workspaceId: string; onClose: () => void }) {
  const imp = useImportVariables(workspaceId)
  const [content, setContent] = useState('')
  const [secrets, setSecrets] = useState<'auto' | 'all' | 'none'>('auto')
  const [overwrite, setOverwrite] = useState(false)
  const err = imp.error instanceof ApiError ? imp.error : null
  const r = imp.data
  const run = (dryRun: boolean) => imp.mutate({ content, secrets, overwrite, dryRun })

  const onFile = async (f?: File) => { if (f) setContent(await f.text()) }

  return (
    <Modal title="Import a .env file" onClose={onClose} width={560}
      footer={<>
        <Button onClick={onClose}>{r && !r.dryRun ? 'Close' : 'Cancel'}</Button>
        <Button disabled={!content.trim()} loading={imp.isPending && imp.variables?.dryRun} onClick={() => run(true)}>Preview</Button>
        <Button variant="primary" disabled={!content.trim() || !r?.dryRun} loading={imp.isPending && imp.variables?.dryRun === false} onClick={() => run(false)}>Import</Button>
      </>}>
      <div className="space-y-4 text-fg">
        <div>
          <label htmlFor="env-content" className="mb-1.5 block text-[13px] font-semibold">Paste the file contents</label>
          <textarea id="env-content" rows={8} value={content} onChange={(e) => { setContent(e.target.value); imp.reset() }} spellCheck={false}
            className="w-full rounded-[10px] border border-line-strong bg-field p-3 font-mono text-[12.5px]" placeholder={'DATABASE_URL=...\nAPI_KEY=...'} />
          <label className="mt-2 inline-block text-xs text-muted">
            or choose a file <input type="file" aria-label="Choose a .env file" className="ml-1 text-xs" onChange={(e) => onFile(e.target.files?.[0])} />
          </label>
        </div>
        <div className="flex flex-wrap items-center gap-4 text-sm">
          <label>Store as secret{' '}
            <select value={secrets} onChange={(e) => { setSecrets(e.target.value as typeof secrets); imp.reset() }} className="ml-1 h-8 rounded-lg border border-line-strong bg-field px-2">
              <option value="auto">names that look secret</option><option value="all">all values</option><option value="none">none</option>
            </select>
          </label>
          <label className="flex items-center gap-2"><input type="checkbox" checked={overwrite} onChange={(e) => { setOverwrite(e.target.checked); imp.reset() }} /> Overwrite existing</label>
        </div>
        {err && <p role="alert" className="rounded-lg bg-err-bg p-3 text-sm text-err-fg">{err.message}</p>}
        {r && (
          <div role="status" className="rounded-lg border border-line bg-raised p-3 text-sm">
            <p className="font-semibold">{r.dryRun ? 'Preview' : 'Imported'}: {r.created.length} new, {r.updated.length} updated, {r.skipped.length} skipped{r.secrets.length ? `, ${r.secrets.length} secret` : ''}</p>
            {r.skipped.length > 0 && <p className="mt-1 text-xs text-muted">Skipped (already set): {r.skipped.join(', ')}</p>}
            {r.errors.map((e) => <p key={e.line} className="mt-1 text-xs text-err-fg">Line {e.line}: {e.message}</p>)}
          </div>
        )}
      </div>
    </Modal>
  )
}
