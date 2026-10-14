import { useState } from 'react'
import { ApiError } from '@/api/client'
import type { CatalogComponent } from '@/api/types'
import { Button } from '@/components/ui/Button'
import { Modal } from '@/components/ui/Modal'
import { Monogram } from '@/features/canvas/TechNode'
import { useImportConnections, type ImportCandidate } from './hooks'

interface Props {
  /** Restrict results to one technology (the Connect tab of a card). */
  catalogId?: string
  defs: Map<string, CatalogComponent>
  onPick: (c: ImportCandidate) => void
  onClose: () => void
}

const FORMAT: Record<string, string> = { 'connection-string': 'connection string', env: '.env file', compose: 'docker-compose file' }

/**
 * Reads a .env file, a connection string or a docker-compose file and lists
 * the connections found. Choosing one only fills the Connect form for review.
 */
export function ImportConnectionDialog({ catalogId, defs, onPick, onClose }: Props) {
  const imp = useImportConnections()
  const [text, setText] = useState('')
  const err = imp.error instanceof ApiError ? imp.error : null

  return (
    <Modal title="Import connection details" width={640} onClose={onClose} footer={<Button onClick={onClose}>Close</Button>}>
      <div className="space-y-4 text-fg">
        <div>
          <label htmlFor="imp-text" className="mb-1.5 block text-[13px] font-semibold">Paste a connection string, a .env file or a docker-compose file</label>
          <textarea id="imp-text" rows={7} value={text} spellCheck={false} autoComplete="off" onChange={(e) => { setText(e.target.value); imp.reset() }}
            className="w-full rounded-[10px] border border-line-strong bg-field p-3 font-mono text-[12px]" placeholder={'DATABASE_URL=mysql://user:password@host:3306/db\n…or the services: section of a compose file'} />
          <div className="mt-2 flex items-center gap-3">
            <label className="text-xs text-muted">or choose a file <input type="file" aria-label="Choose a file to import" className="ml-1 text-xs" onChange={async (e) => { const f = e.target.files?.[0]; if (f) { setText(await f.text()); imp.reset() } }} /></label>
            <Button variant="primary" size="sm" className="ml-auto" disabled={!text.trim()} loading={imp.isPending} onClick={() => imp.mutate({ content: text, catalogId })}>Find connections</Button>
          </div>
          <p className="mt-2 text-xs text-muted">Nothing is saved. You review the values on the Connect tab, and Test Connection is still required.</p>
        </div>
        {err && <p role="alert" className="rounded-lg bg-err-bg p-3 text-sm text-err-fg">{err.message}</p>}
        {imp.data && (
          <div>
            <p role="status" className="mb-2 text-sm font-semibold">Found {imp.data.candidates.length} {imp.data.candidates.length === 1 ? 'connection' : 'connections'} in the {FORMAT[imp.data.format]}</p>
            <ul className="space-y-2">
              {imp.data.candidates.map((c, i) => {
                const def = defs.get(c.catalogId)
                return (
                  <li key={c.source + i} className="flex items-start gap-3 rounded-card border border-line bg-panel p-3">
                    <Monogram text={def?.monogram ?? '?'} size={32} />
                    <div className="min-w-0 flex-1 text-[13px]">
                      <p className="font-bold">{def?.name ?? c.catalogId} <span className="font-normal text-muted">from {c.source}</span></p>
                      <p className="mt-0.5 break-words font-mono text-[12px] text-muted">
                        {Object.entries(c.fields).filter(([k]) => !c.secrets.includes(k)).map(([k, v]) => `${k}=${String(v)}`).join('  ')}
                        {c.secrets.length > 0 && `  ${c.secrets.map((s) => `${s}=••••`).join('  ')}`}
                      </p>
                      {c.missing.length > 0 && <p className="mt-1 text-xs text-warn-fg">Not found in the input, enter it yourself: {c.missing.join(', ')}</p>}
                      {c.notes?.map((n) => <p key={n} className="mt-1 text-xs text-muted">{n}</p>)}
                    </div>
                    <Button size="sm" variant="primary" aria-label={`Use ${c.source}`} onClick={() => onPick(c)}>Use</Button>
                  </li>
                )
              })}
            </ul>
            {imp.data.skipped.length > 0 && (
              <details className="mt-3 text-xs text-muted"><summary className="cursor-pointer">{imp.data.skipped.length} item{imp.data.skipped.length === 1 ? '' : 's'} skipped</summary>
                <ul className="mt-1 list-disc pl-5">{imp.data.skipped.map((s, i) => <li key={i}><span className="font-mono">{s.source}</span>: {s.reason}</li>)}</ul>
              </details>
            )}
          </div>
        )}
      </div>
    </Modal>
  )
}
