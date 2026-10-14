import clsx from 'clsx'
import { useState } from 'react'
import { ApiError } from '@/api/client'
import type { CatalogComponent } from '@/api/types'
import { Button } from '@/components/ui/Button'
import { Field } from '@/components/ui/Field'
import { Modal } from '@/components/ui/Modal'
import { Monogram } from '@/features/canvas/TechNode'
import { useTemplateActions, useTemplates, type TemplateEntry } from './hooks'

interface Props {
  workspaceId: string
  defs: Map<string, CatalogComponent>
  canvasHasCards: boolean
  onClose: () => void
}

/** Stack template gallery: curated starters and the user's own saved canvases. */
export function TemplatesDialog({ workspaceId, defs, canvasHasCards, onClose }: Props) {
  const { data, isPending, error } = useTemplates()
  const acts = useTemplateActions(workspaceId)
  const [tab, setTab] = useState<'curated' | 'personal'>('curated')
  const [picked, setPicked] = useState<TemplateEntry | null>(null)
  const [done, setDone] = useState<{ added: number; skipped: string[] } | null>(null)
  const [saving, setSaving] = useState(false)
  const list = data ? data[tab] : []
  const nameOf = (id: string) => defs.get(id)?.name ?? id

  const apply = () => picked && acts.apply.mutate(picked.id, { onSuccess: (r) => setDone({ added: r.added.length, skipped: r.skipped }) })

  if (done) {
    return (
      <Modal title="Template added" onClose={onClose} footer={<Button variant="primary" onClick={onClose}>Review the cards</Button>}>
        <p role="status" className="text-fg">Added {done.added} {done.added === 1 ? 'card' : 'cards'} as drafts. Open each one to choose Install or Connect. Nothing has been installed.</p>
        {done.skipped.length > 0 && <p className="mt-2">Already on your canvas, so not added again: {done.skipped.map(nameOf).join(', ')}.</p>}
      </Modal>
    )
  }

  return (
    <Modal title="Stack templates" width={720} onClose={onClose}
      footer={<>
        <Button onClick={onClose}>Close</Button>
        {picked && <Button variant="primary" loading={acts.apply.isPending} onClick={apply}>Add {picked.components.length} cards to canvas</Button>}
      </>}>
      <div className="space-y-4 text-fg">
        <div role="tablist" aria-label="Template sets" className="flex border-b border-line">
          {([['curated', 'Starter stacks'], ['personal', 'My templates']] as const).map(([k, l]) => (
            <button key={k} role="tab" aria-selected={tab === k} onClick={() => { setTab(k); setPicked(null) }}
              className={clsx('-mb-px mr-5 border-b-2 pb-2 text-sm font-semibold', tab === k ? 'border-blue text-fg' : 'border-transparent text-muted hover:text-fg')}>{l}</button>
          ))}
        </div>
        {isPending && <p className="text-muted">Loading…</p>}
        {error instanceof ApiError && <p role="alert" className="text-err-fg">{error.message}</p>}
        {data && list.length === 0 && (
          <p className="rounded-card border border-dashed border-line-strong p-6 text-center text-sm text-muted">
            {tab === 'personal' ? 'You have not saved any templates yet. Save the current canvas to reuse it.' : 'No starter stacks available.'}
          </p>
        )}
        <ul className="grid gap-3 sm:grid-cols-2">
          {list.map((t) => (
            <li key={t.id}>
              <button type="button" aria-pressed={picked?.id === t.id} onClick={() => setPicked(t)}
                className={clsx('w-full rounded-card border p-4 text-left transition-colors', picked?.id === t.id ? 'border-blue bg-raised' : 'border-line bg-panel hover:border-line-strong')}>
                <span className="block font-bold">{t.name}</span>
                <span className="mt-0.5 block text-[13px] text-muted">{t.description}</span>
                <span className="mt-3 flex flex-wrap gap-1.5" aria-label={`Components in ${t.name}`}>
                  {t.components.map((c, i) => (
                    <span key={i} className="inline-flex items-center gap-1.5 rounded-lg border border-line bg-bg py-1 pl-1 pr-2 text-xs">
                      <Monogram text={defs.get(c.id)?.monogram ?? '?'} size={20} />{nameOf(c.id)}
                    </span>
                  ))}
                </span>
              </button>
              {t.personal && <button type="button" aria-label={`Delete template ${t.name}`} className="mt-1 text-xs text-muted hover:text-err-fg" onClick={() => acts.remove.mutate(t.id, { onSuccess: () => setPicked((p) => (p?.id === t.id ? null : p)) })}>Delete</button>}
            </li>
          ))}
        </ul>
        {picked && <p className="rounded-lg bg-raised p-3 text-[13px] text-muted">Cards are added as <strong className="text-fg">drafts</strong>{canvasHasCards ? ' below what is already on your canvas' : ''}. You choose Install or Connect for each, and confirm. Nothing installs automatically.</p>}
        {acts.apply.error instanceof ApiError && <p role="alert" className="text-sm text-err-fg">{acts.apply.error.message}</p>}
        <div className="border-t border-line pt-3">
          {!saving ? (
            <Button size="sm" disabled={!canvasHasCards} onClick={() => setSaving(true)}>Save current canvas as a template…</Button>
          ) : <SaveForm workspaceId={workspaceId} onDone={() => { setSaving(false); setTab('personal') }} onCancel={() => setSaving(false)} />}
          {!canvasHasCards && <span className="ml-3 text-xs text-faint">Add components to the canvas first.</span>}
        </div>
      </div>
    </Modal>
  )
}

function SaveForm({ workspaceId, onDone, onCancel }: { workspaceId: string; onDone: () => void; onCancel: () => void }) {
  const { save } = useTemplateActions(workspaceId)
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const err = save.error instanceof ApiError ? save.error : null
  return (
    <form className="space-y-3" onSubmit={(e) => { e.preventDefault(); save.mutate({ name, description }, { onSuccess: onDone }) }}>
      <Field label="Template name" value={name} onChange={(e) => setName(e.target.value)} maxLength={60} error={err?.field === 'name' ? err.message : undefined} />
      <Field label="Description" optional value={description} onChange={(e) => setDescription(e.target.value)} error={err?.field === 'description' ? err.message : undefined} />
      <p className="text-xs text-muted">Saves which technologies are on the canvas, where, and whether each is installed or connected. Connection details, names and secrets are never saved.</p>
      <div className="flex gap-2"><Button type="submit" variant="primary" size="sm" disabled={!name.trim()} loading={save.isPending}>Save template</Button><Button type="button" size="sm" onClick={onCancel}>Cancel</Button></div>
    </form>
  )
}
