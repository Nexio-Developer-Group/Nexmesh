import clsx from 'clsx'
import { useEffect, useMemo, useState } from 'react'
import type { CanvasComponent, CatalogComponent } from '@/api/types'
import { ApiError } from '@/api/client'
import { Button } from '@/components/ui/Button'
import { Modal } from '@/components/ui/Modal'
import { DRILL, useInspect, useInspectorCaps, type InspectResult, type Op } from './hooks'

function Cell({ v }: { v: unknown }) {
  if (v === null || v === undefined) return <span className="text-faint">null</span>
  return <>{String(v)}</>
}

/**
 * The database and broker tools on a connected card (PRD F13). Read-only: the
 * server refuses anything that would change data, runs every query with a row
 * limit and a timeout, and records each request (never the results) in the audit log.
 */
export function InspectorDialog({ workspaceId, component: c, def, onClose }: { workspaceId: string; component: CanvasComponent; def: CatalogComponent; onClose: () => void }) {
  const { data: caps, isPending } = useInspectorCaps(workspaceId, c.id)
  const run = useInspect(workspaceId, c.id)
  const [opName, setOpName] = useState('')
  const [args, setArgs] = useState<Record<string, string>>({})
  const [result, setResult] = useState<InspectResult | null>(null)
  const [shownOp, setShownOp] = useState('')
  const ops = caps?.ops ?? []
  const op: Op | undefined = ops.find((o) => o.name === opName) ?? ops[0]
  useEffect(() => { if (ops.length && !opName) setOpName(ops[0].name) }, [ops, opName])
  const drill = useMemo(() => (shownOp ? DRILL[def.id]?.[shownOp] : undefined), [def.id, shownOp])

  const exec = (o: string, a: Record<string, string>) => {
    run.mutate({ op: o, args: Object.fromEntries(Object.entries(a).filter(([, v]) => v !== '')) }, { onSuccess: (r) => { setResult(r); setShownOp(o) } })
  }
  const go = (value: unknown) => {
    if (!drill) return
    const keep: Record<string, string> = { ...args }
    // Carry the database along when drilling from tables to a table.
    if (shownOp === 'tables' || shownOp === 'collections') keep.database = args.database ?? ''
    keep[drill.arg] = String(value)
    setOpName(drill.op)
    setArgs(keep)
    exec(drill.op, drill.op === 'find' ? { ...keep, limit: '' } : keep)
  }
  const err = run.error instanceof ApiError ? run.error : null
  const prod = caps?.production || result?.production

  return (
    <Modal title={`Inspect ${c.connName || def.name}`} onClose={onClose} width={900}
      footer={<Button onClick={onClose}>Close</Button>}>
      {isPending && <p>Loading…</p>}
      {caps && !caps.available && <p role="status" className="rounded-lg bg-neutral-bg px-3 py-2 text-fg">{caps.reason}</p>}
      {caps?.available && op && (
        <div className="space-y-4 text-fg">
          <div className="flex flex-wrap items-center gap-2 text-xs">
            <span className="rounded-md bg-ok-bg px-2 py-1 font-semibold text-ok-fg">✓ Read-only</span>
            <span className="text-muted">Writes are refused. Results are not stored; each request is recorded in the audit log without them.</span>
          </div>
          {prod && <p role="note" className="rounded-lg border border-warn-fg/40 bg-warn-bg px-3 py-2 text-[13px] text-warn-fg"><strong>▲ Production.</strong> You are looking at a production-labelled connection. Take care what you copy.</p>}

          <form className="space-y-3" onSubmit={(e) => { e.preventDefault(); exec(op.name, args) }}>
            <div>
              <label htmlFor="insp-op" className="mb-1.5 block text-[13px] font-semibold">Operation</label>
              <select id="insp-op" value={op.name} onChange={(e) => { setOpName(e.target.value); setResult(null) }}
                className="h-10 w-full max-w-sm rounded-[10px] border border-line-strong bg-field px-3 text-sm">
                {ops.map((o) => <option key={o.name} value={o.name}>{o.label}</option>)}
              </select>
            </div>
            <div className="grid gap-3 sm:grid-cols-2">
              {(op.args ?? []).map((a) => (
                <div key={a.name} className={a.kind === 'code' ? 'sm:col-span-2' : ''}>
                  <label htmlFor={`insp-${a.name}`} className="mb-1.5 block text-[13px] font-semibold">{a.label}{!a.required && <span className="ml-1 font-normal text-faint">optional</span>}</label>
                  {a.kind === 'code' ? (
                    <textarea id={`insp-${a.name}`} rows={3} spellCheck={false} value={args[a.name] ?? ''} placeholder={a.hint}
                      onChange={(e) => setArgs((p) => ({ ...p, [a.name]: e.target.value }))}
                      className="w-full rounded-[10px] border border-line-strong bg-field p-3 font-mono text-[12.5px]" />
                  ) : (
                    <input id={`insp-${a.name}`} type={a.kind === 'number' ? 'number' : 'text'} value={args[a.name] ?? ''} placeholder={a.hint}
                      onChange={(e) => setArgs((p) => ({ ...p, [a.name]: e.target.value }))}
                      className="h-10 w-full rounded-[10px] border border-line-strong bg-field px-3 font-mono text-[13px]" />
                  )}
                </div>
              ))}
            </div>
            <Button type="submit" variant="primary" loading={run.isPending} disabled={(op.args ?? []).some((a) => a.required && !(args[a.name] ?? '').trim())}>Run</Button>
          </form>

          {err && <p role="alert" className="rounded-lg bg-err-bg px-3 py-2 text-[13px] text-err-fg">{err.message}</p>}

          {result && (
            <section aria-label="Results">
              <p className="text-xs text-muted">
                {result.rows.length} row{result.rows.length === 1 ? '' : 's'} · {result.elapsedMs} ms
                {drill && result.rows.length > 0 && <span> · click a {result.columns[0]} to continue</span>}
              </p>
              {result.note && <p className="mt-1 rounded-lg bg-info-bg px-3 py-1.5 text-xs text-info-fg">{result.note}</p>}
              {result.rows.length === 0 ? <p className="mt-2 text-sm text-muted">Nothing found.</p> : (
                <div className="mt-2 max-h-[340px] overflow-auto rounded-[10px] border border-line">
                  <table className="w-full text-left font-mono text-[12px]">
                    <thead className="sticky top-0 bg-raised text-[11px] uppercase tracking-wider text-faint">
                      <tr>{result.columns.map((col) => <th key={col} scope="col" className="px-3 py-2 font-semibold">{col}</th>)}</tr>
                    </thead>
                    <tbody>
                      {result.rows.map((row, i) => (
                        <tr key={i} className="border-t border-line align-top">
                          {row.map((v, j) => (
                            <td key={j} className={clsx('max-w-[420px] whitespace-pre-wrap break-words px-3 py-1.5', j === 0 && drill && 'p-0')}>
                              {j === 0 && drill ? (
                                <button type="button" onClick={() => go(v)} className="w-full px-3 py-1.5 text-left text-link hover:underline"><Cell v={v} /></button>
                              ) : <Cell v={v} />}
                            </td>
                          ))}
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
              {result.next && (
                <Button className="mt-2" size="sm" loading={run.isPending} onClick={() => exec(shownOp, { ...args, cursor: result.next! })}>Next page</Button>
              )}
              {result.truncated && !result.next && <p className="mt-2 text-xs text-muted">More exists than is shown.</p>}
            </section>
          )}
        </div>
      )}
    </Modal>
  )
}
