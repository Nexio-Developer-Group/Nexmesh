import clsx from 'clsx'
import { useEffect, useId, useMemo, useRef, useState } from 'react'
import { ApiError } from '@/api/client'
import type { CanvasComponent, CatalogComponent, CatalogField } from '@/api/types'
import { Button } from '@/components/ui/Button'
import { Field } from '@/components/ui/Field'
import { Modal } from '@/components/ui/Modal'
import { relativeTime } from '@/lib/format'
import { TestResultList } from './TestResultList'
import {
  useConnections, useDisconnect, useEditConnection, useParseConnectionString, useRetest, useRotate, useSaveConnection, useTestConnection,
  type ConnectionBody,
} from './hooks'

const ENVS = ['development', 'staging', 'production'] as const
const WIDE = new Set(['host', 'bootstrapServers', 'managementUrl', 'schemaRegistryUrl'])

/** Replaces the password in a pasted string so the real value never stays in the DOM. */
export function maskConnectionString(uri: string): string {
  return uri.replace(/^([a-z+]+:\/\/[^:@/\s]*):([^@\s]*)@/i, (_m, head) => `${head}:••••@`)
}

export function ConnectTab({ workspaceId, component: c, def }: { workspaceId: string; component: CanvasComponent; def: CatalogComponent }) {
  const [editing, setEditing] = useState(false)
  useEffect(() => setEditing(false), [c.id, c.mode])
  if (c.mode === 'connected' && !editing) return <ConnectedView workspaceId={workspaceId} component={c} def={def} onEdit={() => setEditing(true)} />
  return <ConnectForm workspaceId={workspaceId} component={c} def={def} editing={c.mode === 'connected'} onCancel={() => setEditing(false)} onSaved={() => setEditing(false)} />
}

function initialFields(def: CatalogComponent, c: CanvasComponent): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const f of def.connection?.fields ?? []) {
    if (f.type === 'password') continue
    if (f.default !== undefined) out[f.name] = f.default
  }
  return { ...out, ...(c.connection ?? {}) }
}

function ConnectForm({ workspaceId, component: c, def, editing, onCancel, onSaved }: {
  workspaceId: string; component: CanvasComponent; def: CatalogComponent; editing: boolean; onCancel: () => void; onSaved: () => void
}) {
  const fieldsDef = def.connection?.fields ?? []
  const [connName, setConnName] = useState(c.connName ?? '')
  const [environment, setEnvironment] = useState<string>(c.environment || 'development')
  const [fields, setFields] = useState<Record<string, unknown>>(() => initialFields(def, c))
  const [secrets, setSecrets] = useState<Record<string, string>>({})
  const [uri, setUri] = useState('')
  const [uriError, setUriError] = useState<string | undefined>()
  const test = useTestConnection(workspaceId, c.id)
  const save = useSaveConnection(workspaceId, c.id)
  const edit = useEditConnection(workspaceId, c.id)
  const parse = useParseConnectionString()

  const body = useMemo<ConnectionBody>(() => ({ connName, environment, fields: { ...fields, ...Object.fromEntries(Object.entries(secrets).filter(([, v]) => v !== '')) } }), [connName, environment, fields, secrets])
  const key = JSON.stringify(body)
  const heldSecret = !!c.lastTest?.passed && c.observedState === 'test_passed'
  const [passedKey, setPassedKey] = useState<string | null>(() => (!editing && heldSecret ? key : null))
  const canAdd = passedKey === key && !save.isPending && !edit.isPending

  // Pasting a connection string fills the individual fields for review.
  const lastParsed = useRef('')
  useEffect(() => {
    if (!uri || uri.includes('••••') || uri === lastParsed.current) return
    const t = setTimeout(() => {
      lastParsed.current = uri
      parse.mutate({ catalogId: def.id, uri }, {
        onSuccess: ({ fields: parsed }) => {
          setUriError(undefined)
          const plain: Record<string, unknown> = {}
          const sec: Record<string, string> = {}
          for (const f of fieldsDef) {
            const v = parsed[f.name]
            if (v === undefined) continue
            if (f.type === 'password') sec[f.name] = String(v)
            else plain[f.name] = v
          }
          setFields((p) => ({ ...p, ...plain }))
          setSecrets((p) => ({ ...p, ...sec }))
          setUri(maskConnectionString(uri))
        },
        onError: (e) => setUriError(e.message),
      })
    }, 350)
    return () => clearTimeout(t)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [uri])

  const set = (name: string, v: unknown) => setFields((p) => ({ ...p, [name]: v }))
  const testErr = test.error instanceof ApiError ? test.error : null
  const saveErr = (save.error ?? edit.error) as ApiError | null
  const running = test.isPending
  const result = running ? c.lastTest : (test.data ?? c.lastTest)

  const runTest = () => test.mutate(body, { onSuccess: (r) => setPassedKey(r.passed ? key : null) })
  const submit = () => (editing ? edit : save).mutate(body, { onSuccess: onSaved })

  const basic = fieldsDef.filter((f) => !f.advanced)
  const advanced = fieldsDef.filter((f) => f.advanced)
  const tlsOn = fields.tls === true || fields.srv === true || ['SSL', 'SASL_SSL'].includes(String(fields.securityProtocol)) || (!!fields.sslMode && fields.sslMode !== 'disabled')

  return (
    <div className="flex min-h-full flex-col">
      <div className="flex-1 space-y-5 p-5">
        {environment === 'production' && (
          <div role="note" className="rounded-xl border border-warn-fg/40 bg-warn-bg p-3 text-[13px] text-warn-fg">
            <strong>▲ Production label.</strong> Use a dedicated least-privilege user limited to this project's database.
          </div>
        )}
        {editing && <p className="rounded-lg bg-info-bg p-2.5 text-xs text-info-fg">Editing a live connection. Changes apply only after Test Connection passes.</p>}

        <Field label="Connection name" mono value={connName} onChange={(e) => setConnName(e.target.value.toLowerCase())} placeholder={`prod-${def.id}`} disabled={editing}
          error={saveErr?.field === 'connName' ? saveErr.message : undefined} hint={editing ? 'The name cannot change after it is added.' : 'Lowercase letters, digits, hyphens. Used to name the variables projects receive.'} />

        <fieldset>
          <legend className="mb-1.5 text-[13px] font-semibold">Environment</legend>
          <div role="radiogroup" aria-label="Environment" className="grid grid-cols-3 rounded-[10px] border border-line-strong bg-field p-1">
            {ENVS.map((e) => (
              <button key={e} type="button" role="radio" aria-checked={environment === e} onClick={() => setEnvironment(e)}
                className={clsx('rounded-md py-1.5 text-[13px] font-semibold', environment === e ? (e === 'production' ? 'bg-warn-bg text-warn-fg' : 'bg-raised text-fg') : 'text-muted hover:text-fg')}>{e}</button>
            ))}
          </div>
        </fieldset>

        {def.connection?.schemes && def.connection.schemes.length > 0 && (
          <Field label="Paste connection string" optional mono value={uri} onChange={(e) => { setUri(e.target.value); setUriError(undefined) }}
            placeholder={`${def.connection.schemes[0]}://user:password@host:${def.connection.defaultPort ?? ''}/db`} autoComplete="off" spellCheck={false}
            error={uriError} hint={parse.isPending ? 'Parsing…' : 'Parsed into the fields below. Review each value.'} />
        )}

        <div className="grid grid-cols-2 gap-4">
          {basic.map((f) => (
            <FormField key={f.name} f={f} value={f.type === 'password' ? secrets[f.name] ?? '' : fields[f.name]}
              stored={f.type === 'password' && (editing || heldSecret)}
              onChange={(v) => (f.type === 'password' ? setSecrets((p) => ({ ...p, [f.name]: String(v) })) : set(f.name, v))} />
          ))}
        </div>
        {basic.some((f) => f.type === 'password') && <p className="-mt-2 text-xs text-muted">Passwords are write-only after saving. You can replace them but not view them.</p>}

        {advanced.length > 0 && (
          <details className="group rounded-[10px] border border-line-strong bg-field">
            <summary className="flex cursor-pointer list-none items-center justify-between px-3 py-2.5 text-sm">
              <span className="font-medium">Advanced <span className="ml-2 text-xs text-muted">{advanced.slice(0, 3).map((f) => f.label.toLowerCase()).join(', ')}</span></span>
              <span className="flex items-center gap-2"><span className={clsx('rounded px-1.5 py-0.5 font-mono text-[10px] font-semibold', tlsOn ? 'bg-ok-bg text-ok-fg' : 'bg-neutral-bg text-neutral-fg')}>TLS {tlsOn ? 'on' : 'off'}</span><span aria-hidden className="transition-transform group-open:rotate-90">›</span></span>
            </summary>
            <div className="grid grid-cols-2 gap-4 border-t border-line p-3">
              {advanced.map((f) => (
                <FormField key={f.name} f={f} value={f.type === 'password' ? secrets[f.name] ?? '' : fields[f.name]} stored={false}
                  onChange={(v) => set(f.name, v)} />
              ))}
              {fields.tlsInsecure === true && <p role="alert" className="col-span-2 rounded-lg bg-warn-bg p-2.5 text-xs text-warn-fg">Certificate verification is off. The connection is encrypted but the server's identity is not checked.</p>}
            </div>
          </details>
        )}

        {testErr && <p role="alert" className="rounded-lg bg-err-bg p-3 text-sm text-err-fg">{testErr.code === 'rate_limited' ? 'Too many tests. Wait a few seconds and try again.' : testErr.message}</p>}
        <TestResultList result={result} running={running} />
        {saveErr && saveErr.field !== 'connName' && <p role="alert" className="rounded-lg bg-err-bg p-3 text-sm text-err-fg">{saveErr.message}</p>}
      </div>

      <div className="sticky bottom-0 border-t border-line bg-panel p-4">
        <div className="grid grid-cols-2 gap-3">
          <Button onClick={runTest} loading={running}>{result?.at ? 'Retest' : 'Test Connection'}</Button>
          <Button variant="primary" disabled={!canAdd} loading={save.isPending || edit.isPending} onClick={submit}>{editing ? 'Save changes' : 'Add to Workspace'}</Button>
        </div>
        <p className="mt-2 text-center text-xs text-muted">
          {canAdd ? 'Test passed for these exact values.' : passedKey ? 'Values changed since the last test. Test again to continue.' : 'Test Connection must pass before you can add this.'}
        </p>
        {editing && <button className="mx-auto mt-2 block text-xs text-muted hover:text-fg" onClick={onCancel}>Cancel editing</button>}
      </div>
    </div>
  )
}

function FormField({ f, value, onChange, stored }: { f: CatalogField; value: unknown; onChange: (v: unknown) => void; stored: boolean }) {
  const id = useId()
  const wide = WIDE.has(f.name) || f.type === 'textarea' || f.type === 'boolean'
  const cls = wide ? 'col-span-2' : ''
  if (f.type === 'boolean') {
    return (
      <label className={clsx('flex items-start gap-2.5 text-sm', cls)}>
        <input type="checkbox" role="switch" checked={value === true} onChange={(e) => onChange(e.target.checked)} className="mt-0.5 size-4 accent-blue" />
        <span><span className="font-semibold">{f.label}</span>{f.help && <span className="block text-xs text-muted">{f.help}</span>}</span>
      </label>
    )
  }
  if (f.type === 'select') {
    return (
      <div className={cls}>
        <label htmlFor={id} className="mb-1.5 block text-[13px] font-semibold">{f.label}</label>
        <select id={id} value={String(value ?? '')} onChange={(e) => onChange(e.target.value)} className="h-10 w-full rounded-[10px] border border-line-strong bg-field px-3 font-mono text-[13px]">
          <option value="">{f.required ? 'Select…' : 'Default'}</option>
          {f.options?.map((o) => <option key={o}>{o}</option>)}
        </select>
      </div>
    )
  }
  if (f.type === 'textarea') {
    return (
      <div className={cls}>
        <label htmlFor={id} className="mb-1.5 block text-[13px] font-semibold">{f.label} <span className="font-normal text-faint">optional</span></label>
        <textarea id={id} rows={3} value={String(value ?? '')} onChange={(e) => onChange(e.target.value)} spellCheck={false} placeholder="-----BEGIN CERTIFICATE-----"
          className="w-full rounded-[10px] border border-line-strong bg-field p-3 font-mono text-[11px]" />
        {f.help && <p className="mt-1 text-xs text-muted">{f.help}</p>}
      </div>
    )
  }
  const isPw = f.type === 'password'
  return (
    <Field className={cls} label={f.label} optional={!f.required} mono={f.type !== 'string' || WIDE.has(f.name)} type={isPw ? 'password' : f.type === 'number' ? 'number' : 'text'}
      value={value === undefined || value === null ? '' : String(value)} autoComplete={isPw ? 'new-password' : 'off'} spellCheck={false}
      placeholder={isPw ? (stored ? '•••••••• (stored)' : '') : f.placeholder} hint={isPw && stored ? 'Stored. Leave empty to keep it.' : undefined}
      onChange={(e) => onChange(f.type === 'number' ? (e.target.value === '' ? '' : Number(e.target.value)) : e.target.value)} />
  )
}

function ConnectedView({ workspaceId, component: c, def, onEdit }: { workspaceId: string; component: CanvasComponent; def: CatalogComponent; onEdit: () => void }) {
  const retest = useRetest(workspaceId, c.id)
  const disconnect = useDisconnect(workspaceId, c.id)
  const conns = useConnections(workspaceId, true, [c.connName, c.observedState])
  const exposed = conns.data?.find((x) => x.id === c.id)?.exposedAs ?? []
  const [rotate, setRotate] = useState(false)
  const [confirmDisconnect, setConfirmDisconnect] = useState(false)
  const conn = c.connection ?? {}
  const rows: [string, string][] = [
    ['Name', c.connName ?? ''], ['Environment', c.environment || '—'],
    ...(def.connection?.fields ?? []).filter((f) => f.type !== 'password' && !f.advanced && conn[f.name] !== undefined && conn[f.name] !== '').map((f): [string, string] => [f.label, String(conn[f.name])]),
    ...(def.connection?.fields ?? []).filter((f) => f.type === 'password').map((f): [string, string] => [f.label, 'Stored · write-only']),
    ['Server', [def.name, c.lastTest?.serverVersion].filter(Boolean).join(' ')],
  ]
  const degraded = c.observedState === 'degraded'
  return (
    <div className="space-y-5 p-5">
      <div role="status" className={clsx('rounded-xl p-3 text-[13px]', degraded ? 'bg-warn-bg text-warn-fg' : 'bg-ok-bg text-ok-fg')}>
        {degraded ? '▲ ' : '✓ '}{c.message || 'Connected.'}
        {c.updatedAt && <span className="ml-1 opacity-80">· {relativeTime(String(c.updatedAt))}</span>}
      </div>
      <dl className="space-y-2 text-sm">
        {rows.map(([k, v]) => <div key={k} className="flex justify-between gap-4"><dt className="text-muted">{k}</dt><dd className="break-all text-right font-mono text-[12.5px]">{v}</dd></div>)}
      </dl>
      {exposed.length > 0 && (
        <div className="rounded-xl bg-neutral-bg p-3">
          <p className="mb-1.5 text-xs font-semibold">Available to projects as</p>
          <p className="flex flex-wrap gap-1.5">{exposed.map((n) => <code key={n} className="rounded bg-field px-1.5 py-0.5 font-mono text-[11px]">{n}</code>)}</p>
          <p className="mt-2 text-[11px] text-muted">Set only on processes started through the workspace agent. Variable naming is provisional.</p>
        </div>
      )}
      {c.lastTest && <TestResultList result={retest.data ?? c.lastTest} running={retest.isPending} />}
      {retest.error && <p role="alert" className="text-sm text-err-fg">{retest.error.message}</p>}
      <div className="grid grid-cols-2 gap-2">
        <Button onClick={() => retest.mutate()} loading={retest.isPending}>Re-test</Button>
        <Button onClick={onEdit}>Edit</Button>
        <Button onClick={() => setRotate(true)}>Rotate credentials</Button>
        <Button variant="danger" onClick={() => setConfirmDisconnect(true)}>Disconnect</Button>
      </div>
      <p className="text-xs text-muted">Disconnect removes stored credentials. It never deletes data on the external service.</p>

      {rotate && <RotateDialog workspaceId={workspaceId} component={c} def={def} onClose={() => setRotate(false)} />}
      {confirmDisconnect && (
        <Modal title={`Disconnect ${c.connName}?`} onClose={() => setConfirmDisconnect(false)}
          footer={<><Button onClick={() => setConfirmDisconnect(false)}>Cancel</Button><Button variant="danger" loading={disconnect.isPending} onClick={() => disconnect.mutate(undefined, { onSuccess: () => setConfirmDisconnect(false) })}>Disconnect</Button></>}>
          <p>The connection and its stored credentials are removed from this workspace. Projects lose the variables listed above. Data on the external service is not touched.</p>
          {disconnect.error && <p role="alert" className="mt-3 text-err-fg">{disconnect.error.message}</p>}
        </Modal>
      )}
    </div>
  )
}

function RotateDialog({ workspaceId, component: c, def, onClose }: { workspaceId: string; component: CanvasComponent; def: CatalogComponent; onClose: () => void }) {
  const rotate = useRotate(workspaceId, c.id)
  const pwFields = (def.connection?.fields ?? []).filter((f) => f.type === 'password')
  const [vals, setVals] = useState<Record<string, string>>({})
  const filled = Object.fromEntries(Object.entries(vals).filter(([, v]) => v))
  return (
    <Modal title="Rotate credentials" onClose={onClose}
      footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" disabled={Object.keys(filled).length === 0} loading={rotate.isPending} onClick={() => rotate.mutate(filled, { onSuccess: onClose })}>Test and replace</Button></>}>
      <p>The new credentials are tested first. The stored ones are replaced only if the test passes.</p>
      <div className="mt-4 space-y-3">
        {pwFields.map((f) => <Field key={f.name} label={`New ${f.label.toLowerCase()}`} type="password" autoComplete="new-password" value={vals[f.name] ?? ''} onChange={(e) => setVals((p) => ({ ...p, [f.name]: e.target.value }))} />)}
      </div>
      {rotate.error && <p role="alert" className="mt-3 rounded-lg bg-err-bg p-2.5 text-err-fg">{rotate.error.message}</p>}
    </Modal>
  )
}
