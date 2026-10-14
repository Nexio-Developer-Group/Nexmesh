import { useState } from 'react'
import { Button } from '@/components/ui/Button'
import { Field } from '@/components/ui/Field'
import { Modal } from '@/components/ui/Modal'
import { useDeleteRule, useRules, useSaveRule, type Rule, type RuleInput, type RuleScope } from './hooks'

const SCOPES: { id: RuleScope; label: string; hint: string }[] = [
  { id: 'global', label: 'Everywhere', hint: 'Applies to every request.' },
  { id: 'component', label: 'A component', hint: 'Applies while that card is on the canvas.' },
  { id: 'path', label: 'Paths', hint: 'Applies to changes that touch matching files.' },
]

const lines = (s: string) => s.split('\n').map((x) => x.trim()).filter(Boolean)

function RuleForm({ workspaceId, rule, onClose }: { workspaceId: string; rule?: Rule; onClose: () => void }) {
  const save = useSaveRule(workspaceId)
  const [title, setTitle] = useState(rule?.title ?? '')
  const [scope, setScope] = useState<RuleScope>(rule?.scope ?? 'global')
  const [components, setComponents] = useState((rule?.components ?? []).join('\n'))
  const [paths, setPaths] = useState((rule?.paths ?? []).join('\n'))
  const [protect, setProtect] = useState((rule?.protect ?? []).join('\n'))
  const [body, setBody] = useState(rule?.body ?? '')
  const input = (): RuleInput => ({ name: rule?.name, title, scope, components: scope === 'component' ? lines(components) : [], paths: scope === 'path' ? lines(paths) : [], protect: lines(protect), body })
  return (
    <Modal title={rule ? `Edit rule: ${rule.title}` : 'New rule'} onClose={onClose} width={640}
      footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" loading={save.isPending} disabled={!title.trim() || !body.trim()} onClick={() => save.mutate(input(), { onSuccess: onClose })}>Save rule</Button></>}>
      <div className="space-y-3">
        <Field label="Title" value={title} onChange={(e) => setTitle(e.target.value)} maxLength={100} />
        <fieldset>
          <legend className="mb-1 text-[13px] font-semibold text-fg">Applies to</legend>
          <div role="radiogroup" aria-label="Scope" className="flex flex-wrap gap-2">
            {SCOPES.map((s) => (
              <label key={s.id} className="cursor-pointer rounded-lg border border-line px-3 py-1.5 text-[13px] has-[:checked]:border-blue has-[:checked]:bg-raised">
                <input type="radio" name="scope" className="mr-2" checked={scope === s.id} onChange={() => setScope(s.id)} />{s.label}
              </label>
            ))}
          </div>
          <p className="mt-1 text-xs text-muted">{SCOPES.find((s) => s.id === scope)?.hint}</p>
        </fieldset>
        {scope === 'component' && (
          <div><label htmlFor="rule-components" className="mb-1 block text-[13px] font-semibold text-fg">Components (one per line)</label>
            <textarea id="rule-components" rows={2} value={components} onChange={(e) => setComponents(e.target.value)} placeholder="redis" className="w-full rounded-lg border border-line-strong bg-field p-2 font-mono text-[12.5px] text-fg" /></div>
        )}
        {scope === 'path' && (
          <div><label htmlFor="rule-paths" className="mb-1 block text-[13px] font-semibold text-fg">Path patterns (one per line)</label>
            <textarea id="rule-paths" rows={2} value={paths} onChange={(e) => setPaths(e.target.value)} placeholder="src/api/**" className="w-full rounded-lg border border-line-strong bg-field p-2 font-mono text-[12.5px] text-fg" /></div>
        )}
        <div><label htmlFor="rule-body" className="mb-1 block text-[13px] font-semibold text-fg">The rule</label>
          <textarea id="rule-body" rows={6} value={body} onChange={(e) => setBody(e.target.value)} placeholder="Describe the convention or constraint in plain language." className="w-full rounded-lg border border-line-strong bg-field p-2 text-[13px] text-fg" /></div>
        <div><label htmlFor="rule-protect" className="mb-1 block text-[13px] font-semibold text-fg">Protected paths <span className="font-normal text-faint">optional, one per line</span></label>
          <textarea id="rule-protect" rows={2} value={protect} onChange={(e) => setProtect(e.target.value)} placeholder="migrations/" className="w-full rounded-lg border border-line-strong bg-field p-2 font-mono text-[12.5px] text-fg" />
          <p className="mt-1 text-xs text-muted">The agent is told not to change these, and a change that touches them shows a warning. Rules guide the agent; they do not block anything. Quality gates do that.</p></div>
        {save.error && <p role="alert" className="text-err-fg">{save.error.message}</p>}
      </div>
    </Modal>
  )
}

/** Agent rules files (PRD F21): ordinary Markdown files in the repository, edited here or in the editor. */
export function RulesTab({ workspaceId }: { workspaceId: string }) {
  const { data, isPending, error } = useRules(workspaceId)
  const del = useDeleteRule(workspaceId)
  const [editing, setEditing] = useState<Rule | 'new' | null>(null)
  const [confirmDel, setConfirmDel] = useState<Rule | null>(null)
  return (
    <div className="max-w-3xl">
      <h1 className="text-xl font-extrabold">Agent rules</h1>
      <p className="mt-1 text-sm text-muted">Durable instructions for the Code Agent. Each rule is a Markdown file under <code className="font-mono">{data?.directory ?? '.nexmesh/rules'}</code>, so it is versioned with your code. An <code className="font-mono">AGENTS.md</code> at the workspace root is also read. Changes apply from the next request.</p>
      <div className="mt-4"><Button variant="primary" onClick={() => setEditing('new')}>New rule</Button></div>
      {isPending && <p className="mt-4 text-sm text-muted">Loading…</p>}
      {error && <p role="alert" className="mt-4 text-sm text-err-fg">{error.message}</p>}
      {data && data.rules.length === 0 && <p className="mt-6 text-sm text-muted">No rules yet. Add one to teach the agent your conventions.</p>}
      <ul className="mt-4 space-y-2" aria-label="Rules">
        {data?.rules.map((r) => (
          <li key={r.id} className="rounded-card border border-line p-3">
            <div className="flex items-center gap-2">
              <span className="font-semibold text-fg">{r.title}</span>
              <span className="rounded-full bg-raised px-2 py-0.5 text-[11px] text-muted">{r.scope === 'global' ? 'everywhere' : r.scope === 'component' ? `component: ${r.components?.join(', ')}` : `paths: ${r.paths?.join(', ')}`}</span>
              {r.readOnly && <span className="text-[11px] text-faint">edit in the editor</span>}
              <div className="ml-auto flex gap-2">
                {!r.readOnly && <Button size="sm" onClick={() => setEditing(r)} aria-label={`Edit ${r.title}`}>Edit</Button>}
                {!r.readOnly && <Button size="sm" variant="danger" onClick={() => setConfirmDel(r)} aria-label={`Delete ${r.title}`}>Delete</Button>}
              </div>
            </div>
            {r.problem && <p role="alert" className="mt-1 text-xs text-err-fg">{r.problem} This rule is not applied.</p>}
            <p className="mt-1 line-clamp-2 whitespace-pre-wrap text-[13px] text-muted">{r.body}</p>
            {r.protect && r.protect.length > 0 && <p className="mt-1 text-xs text-muted">Protected: <span className="font-mono">{r.protect.join(', ')}</span></p>}
          </li>
        ))}
      </ul>
      {editing && <RuleForm workspaceId={workspaceId} rule={editing === 'new' ? undefined : editing} onClose={() => setEditing(null)} />}
      {confirmDel && (
        <Modal title={`Delete “${confirmDel.title}”?`} onClose={() => setConfirmDel(null)}
          footer={<><Button onClick={() => setConfirmDel(null)}>Cancel</Button><Button variant="danger" loading={del.isPending} onClick={() => del.mutate(confirmDel.name, { onSuccess: () => setConfirmDel(null) })}>Delete rule</Button></>}>
          <p>This removes the file {confirmDel.id} from the workspace. If it is committed, Git still has it.</p>
          {del.error && <p role="alert" className="mt-2 text-err-fg">{del.error.message}</p>}
        </Modal>
      )}
    </div>
  )
}
