import clsx from 'clsx'
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { Button } from '@/components/ui/Button'
import { Modal } from '@/components/ui/Modal'
import { ChatView } from './ChatView'
import { ReviewDialog } from './ReviewDialog'
import {
  useAgentContext, useAgentSettings, useChangeSets, useCheckpoints, useRevert, useSaveAgentSettings,
  type AgentLevel, type ChangeSet,
} from './hooks'

const LEVELS: { id: AgentLevel; label: string; hint: string }[] = [
  { id: 'read', label: 'Read only', hint: 'Reads files and the canvas. Cannot propose changes or run commands.' },
  { id: 'propose', label: 'Propose changes', hint: 'Also stages file edits and canvas suggestions for your review.' },
  { id: 'execute', label: 'Propose and run', hint: 'Also runs commands. Read-only and test commands run at once; anything else asks first.' },
]

const STATUS: Record<ChangeSet['status'], string> = {
  proposed: 'Waiting for review', applied: 'Applied', partially_applied: 'Partly applied', rejected: 'Rejected', reverted: 'Reverted', superseded: 'Replaced',
}

export function ContextDialog({ workspaceId, onClose }: { workspaceId: string; onClose: () => void }) {
  const { data, isPending, error } = useAgentContext(workspaceId, true)
  return (
    <Modal title="What the agent can see" onClose={onClose} width={720} footer={<Button onClick={onClose}>Close</Button>}>
      <p>This is the canvas context added to every request. It lists names only: no secret value, host, port or connection string is included.</p>
      {isPending && <p className="mt-3">Loading…</p>}
      {error && <p role="alert" className="mt-3 text-err-fg">{error.message}</p>}
      {data && <pre aria-label="Agent context" className="mt-3 max-h-[50vh] overflow-auto whitespace-pre-wrap rounded-lg border border-line bg-bg p-3 font-mono text-[12px] text-fg">{data.text}</pre>}
    </Modal>
  )
}

function SettingsTab({ workspaceId }: { workspaceId: string }) {
  const { data: s } = useAgentSettings(workspaceId)
  const save = useSaveAgentSettings(workspaceId)
  const [ctx, setCtx] = useState(false)
  if (!s) return <p className="p-4 text-sm text-muted">Loading…</p>
  return (
    <div className="space-y-5 overflow-y-auto p-4 text-[13px]">
      <fieldset>
        <legend className="mb-1.5 font-bold text-fg">What the agent may do</legend>
        <div role="radiogroup" aria-label="Permission level" className="space-y-2">
          {LEVELS.map((l) => (
            <label key={l.id} className={clsx('block cursor-pointer rounded-lg border p-2.5', s.level === l.id ? 'border-blue bg-raised' : 'border-line')}>
              <input type="radio" name="level" className="mr-2" checked={s.level === l.id} onChange={() => save.mutate({ ...s, level: l.id })} />
              <span className="font-semibold text-fg">{l.label}</span>
              <span className="mt-0.5 block pl-5 text-xs text-muted">{l.hint}</span>
            </label>
          ))}
        </div>
      </fieldset>
      <div>
        <label className="flex items-start gap-2">
          <input type="checkbox" className="mt-1" checked={s.autoAccept} disabled={s.level === 'read'} onChange={(e) => save.mutate({ ...s, autoAccept: e.target.checked })} />
          <span><span className="font-semibold text-fg">Apply the agent’s file edits without asking</span>
            <span className="block text-xs text-muted">Off by default. A checkpoint is always recorded so you can revert, and required quality gates still apply.</span></span>
        </label>
      </div>
      {save.error && <p role="alert" className="text-xs text-err-fg">{save.error.message}</p>}
      <div className="flex flex-wrap gap-2">
        <Button size="sm" onClick={() => setCtx(true)}>Show what the agent can see</Button>
        <Link to={`/w/${workspaceId}/settings?tab=agent`}><Button size="sm">Model configuration</Button></Link>
        <Link to={`/w/${workspaceId}/settings?tab=rules`}><Button size="sm">Rules</Button></Link>
        <Link to={`/w/${workspaceId}/settings?tab=gates`}><Button size="sm">Quality gates</Button></Link>
      </div>
      <p className="text-xs text-muted">The agent can never read secret values, change the operating system or resources, delete cards or connections, or install and start components on its own.</p>
      {ctx && <ContextDialog workspaceId={workspaceId} onClose={() => setCtx(false)} />}
    </div>
  )
}

function ChangesTab({ workspaceId, onReview }: { workspaceId: string; onReview: (id: string) => void }) {
  const { data: sets = [] } = useChangeSets(workspaceId)
  const { data: cps = [] } = useCheckpoints(workspaceId)
  const revert = useRevert(workspaceId)
  const [confirm, setConfirm] = useState<{ id: string; label: string; force: boolean } | null>(null)
  const visible = sets.filter((s) => s.status !== 'superseded')
  return (
    <div className="space-y-5 overflow-y-auto p-4 text-[13px]">
      <section aria-label="Change sets">
        <h3 className="font-bold text-fg">Changes</h3>
        {visible.length === 0 ? <p className="mt-1 text-muted">No changes yet. When the agent proposes edits they appear here for review.</p> : (
          <ul className="mt-2 space-y-1.5">
            {visible.map((s) => (
              <li key={s.id} className="flex items-center gap-2 rounded-lg border border-line px-3 py-2">
                <div className="min-w-0 flex-1">
                  <p className="truncate font-semibold text-fg">{s.title}</p>
                  <p className="text-xs text-muted">{s.files.length} file{s.files.length === 1 ? '' : 's'} · {STATUS[s.status]} · {s.actor === 'agent' ? 'agent' : 'you'}</p>
                </div>
                <Button size="sm" variant={s.status === 'proposed' ? 'primary' : 'secondary'} onClick={() => onReview(s.id)} aria-label={`${s.status === 'proposed' ? 'Review' : 'View'} ${s.title}`}>{s.status === 'proposed' ? 'Review' : 'View'}</Button>
              </li>
            ))}
          </ul>
        )}
      </section>
      <section aria-label="Checkpoints">
        <h3 className="font-bold text-fg">Checkpoints</h3>
        <p className="text-xs text-muted">One is saved before every accepted change. Restoring puts the files back as they were.</p>
        {cps.length === 0 ? <p className="mt-1 text-muted">None yet.</p> : (
          <ul className="mt-2 space-y-1.5">
            {cps.map((c) => (
              <li key={c.id} className="flex items-center gap-2 rounded-lg border border-line px-3 py-2">
                <div className="min-w-0 flex-1">
                  <p className="truncate font-semibold text-fg">{c.label}</p>
                  <p className="text-xs text-muted">{c.files.length} file{c.files.length === 1 ? '' : 's'} · {new Date(c.createdAt).toLocaleString()}{c.revertedAt ? ' · restored' : ''}</p>
                </div>
                {!c.revertedAt && <Button size="sm" onClick={() => setConfirm({ id: c.id, label: c.label, force: false })} aria-label={`Restore checkpoint ${c.label}`}>Restore</Button>}
              </li>
            ))}
          </ul>
        )}
      </section>
      {confirm && (
        <Modal title="Restore this checkpoint?" onClose={() => { setConfirm(null); revert.reset() }}
          footer={<>
            <Button onClick={() => { setConfirm(null); revert.reset() }}>Cancel</Button>
            <Button variant="primary" loading={revert.isPending} onClick={() => revert.mutate({ id: confirm.id, force: confirm.force }, { onSuccess: () => setConfirm(null) })}>
              {confirm.force ? 'Restore and overwrite my edits' : 'Restore files'}</Button></>}>
          <p>The files changed by “{confirm.label}” go back to how they were before it was applied.</p>
          {revert.error && (
            <div role="alert" className="mt-3 rounded-lg bg-err-bg px-3 py-2 text-err-fg">
              <p>{revert.error.message}</p>
              {'code' in revert.error && (revert.error as { code?: string }).code === 'edited_since' && !confirm.force && (
                <button className="mt-1 text-xs font-semibold underline" onClick={() => { revert.reset(); setConfirm({ ...confirm, force: true }) }}>Restore anyway</button>
              )}
            </div>
          )}
        </Modal>
      )}
    </div>
  )
}

/** The Code Agent drawer: chat, changes and checkpoints, and permissions (PRD F17, F18). */
export function AgentPanel({ workspaceId, onClose, onHighlight, focusSession }: { workspaceId: string; onClose: () => void; onHighlight: (ids: string[]) => void; focusSession?: string | null }) {
  const [tab, setTab] = useState<'chat' | 'changes' | 'settings'>('chat')
  const [review, setReview] = useState<string | null>(null)
  const { data: sets = [] } = useChangeSets(workspaceId)
  const waiting = sets.filter((s) => s.status === 'proposed').length
  const reviewSet = (id: string) => setReview(id)
  return (
    <aside aria-label="Code Agent" className="flex w-[420px] shrink-0 flex-col border-l border-line bg-panel">
      <div className="flex items-center gap-2 border-b border-line px-4 py-3">
        <h2 className="text-[15px] font-extrabold">Code Agent</h2>
        <button aria-label="Close Code Agent" onClick={onClose} className="ml-auto text-lg text-muted hover:text-fg">×</button>
      </div>
      <div role="tablist" aria-label="Code Agent sections" className="flex border-b border-line px-4">
        {([['chat', 'Chat'], ['changes', 'Changes'], ['settings', 'Settings']] as const).map(([k, label]) => (
          <button key={k} role="tab" aria-selected={tab === k} onClick={() => setTab(k)}
            className={clsx('-mb-px mr-5 border-b-2 pb-2 pt-2 text-[13px] font-semibold', tab === k ? 'border-blue text-fg' : 'border-transparent text-muted hover:text-fg')}>
            {label}{k === 'changes' && waiting > 0 && <span aria-label={`${waiting} waiting`} className="ml-1.5 rounded-full bg-blue px-1.5 py-0.5 text-[10px] text-white">{waiting}</span>}
          </button>
        ))}
      </div>
      <div role="tabpanel" className="flex min-h-0 flex-1 flex-col">
        {tab === 'chat' && <ChatView workspaceId={workspaceId} onReview={reviewSet} focusSession={focusSession} />}
        {tab === 'changes' && <ChangesTab workspaceId={workspaceId} onReview={reviewSet} />}
        {tab === 'settings' && <SettingsTab workspaceId={workspaceId} />}
      </div>
      {review && <ReviewDialog workspaceId={workspaceId} setId={review} onClose={() => setReview(null)} onHighlight={(ids) => { setReview(null); onHighlight(ids) }} />}
    </aside>
  )
}
