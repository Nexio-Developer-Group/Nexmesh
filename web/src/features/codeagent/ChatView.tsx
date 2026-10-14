import clsx from 'clsx'
import { useEffect, useRef, useState } from 'react'
import { Button } from '@/components/ui/Button'
import {
  useApproveCommand, useCreateSession, useDecideProposal, useProposals, useSendMessage, useSession, useSessions, useStopSession,
  type AgentCall, type AgentSession,
} from './hooks'

const STATUS_CHIP: Record<AgentCall['status'], { label: string; cls: string }> = {
  ok: { label: 'Done', cls: 'bg-ok-bg text-ok-fg' },
  error: { label: 'Failed', cls: 'bg-err-bg text-err-fg' },
  denied: { label: 'Not allowed', cls: 'bg-err-bg text-err-fg' },
  needs_approval: { label: 'Working', cls: 'bg-raised text-muted' },
  rejected: { label: 'Declined', cls: 'bg-warn-bg text-warn-fg' },
}

function describe(c: AgentCall): string {
  const a = c.args ?? {}
  const s = (k: string) => (typeof a[k] === 'string' ? (a[k] as string) : '')
  switch (c.name) {
    case 'read_file': return `Read ${s('path')}`
    case 'list_files': return `Listed ${s('path') || 'the workspace'}`
    case 'search_files': return `Searched for “${s('query')}”`
    case 'get_canvas_context': return 'Looked at the canvas'
    case 'propose_changes': return `Proposed changes: ${s('title')}`
    case 'run_command': return `Command: ${s('command')}`
    case 'propose_card': return `Suggested adding ${s('catalog_id')} to the canvas`
    case 'propose_connection': return `Suggested connecting ${s('from')} to ${s('to')}`
    default: return c.name
  }
}

function CallRow({ workspaceId, call, onReview }: { workspaceId: string; call: AgentCall; onReview: (setId: string) => void }) {
  const { data: proposals = [] } = useProposals(workspaceId)
  const decide = useDecideProposal(workspaceId)
  const chip = STATUS_CHIP[call.status] ?? STATUS_CHIP.ok
  const proposal = call.proposalId ? proposals.find((p) => p.id === call.proposalId) : undefined
  return (
    <li className="rounded-lg border border-line bg-bg px-3 py-2 text-[12.5px]">
      <div className="flex items-start gap-2">
        <span className={clsx('mt-0.5 shrink-0 rounded-full px-2 py-0.5 text-[10.5px] font-semibold', chip.cls)}>{chip.label}</span>
        <span className="min-w-0 break-words font-mono text-fg">{describe(call)}</span>
      </div>
      {(call.status === 'denied' || call.status === 'error') && call.result && <p className="mt-1 text-xs text-muted">{call.result}</p>}
      {call.changeSetId && (
        <Button size="sm" className="mt-2" onClick={() => onReview(call.changeSetId!)}>Review changes</Button>
      )}
      {proposal && (
        <div className="mt-2 text-xs">
          <p className="text-muted">{proposal.reason}</p>
          {proposal.status === 'pending' ? (
            <div className="mt-1.5 flex gap-2">
              <Button size="sm" variant="primary" loading={decide.isPending} onClick={() => decide.mutate({ id: proposal.id, accept: true })}>
                {proposal.kind === 'card' ? 'Add as draft card' : 'Create connection'}
              </Button>
              <Button size="sm" onClick={() => decide.mutate({ id: proposal.id, accept: false })}>Dismiss</Button>
            </div>
          ) : (
            <p className="mt-1 font-semibold text-fg">{proposal.status === 'accepted' ? 'Accepted' : proposal.status === 'rejected' ? 'Dismissed' : `Could not apply: ${proposal.error ?? ''}`}</p>
          )}
        </div>
      )}
      {call.name === 'run_command' && call.status === 'ok' && call.result && (
        <details className="mt-1"><summary className="cursor-pointer text-xs text-muted">Output</summary>
          <pre className="mt-1 max-h-40 overflow-auto whitespace-pre-wrap font-mono text-[11px] text-muted">{call.result}</pre></details>
      )}
    </li>
  )
}

function Messages({ workspaceId, session, onReview }: { workspaceId: string; session: AgentSession; onReview: (setId: string) => void }) {
  const end = useRef<HTMLDivElement>(null)
  useEffect(() => { end.current?.scrollIntoView?.({ block: 'end' }) }, [session.turns.length, session.status, session.turns[session.turns.length - 1]?.calls?.length])
  return (
    <div role="log" aria-label="Conversation" aria-live="polite" className="flex-1 space-y-3 overflow-y-auto px-4 py-3">
      {session.turns.length === 0 && <p className="text-[13px] text-muted">Ask the agent about your code or canvas. It proposes changes for you to review; nothing is written until you accept.</p>}
      {session.turns.map((t) => (
        <div key={t.id} className={t.role === 'user' ? 'flex justify-end' : ''}>
          {t.role === 'user' ? (
            <p className="max-w-[88%] whitespace-pre-wrap rounded-[12px] bg-blue px-3 py-2 text-[13px] text-white">{t.content}</p>
          ) : (
            <div className="space-y-2">
              {t.content && <p className="whitespace-pre-wrap text-[13px] text-fg">{t.content}</p>}
              {t.calls && t.calls.length > 0 && (
                <ul className="space-y-1.5" aria-label="Agent actions">{t.calls.map((c) => <CallRow key={c.id} workspaceId={workspaceId} call={c} onReview={onReview} />)}</ul>
              )}
            </div>
          )}
        </div>
      ))}
      {session.status === 'running' && <p role="status" className="text-xs text-muted">The agent is working…</p>}
      {session.status === 'error' && <p role="alert" className="rounded-lg bg-err-bg px-3 py-2 text-[13px] text-err-fg">{session.error}</p>}
      {session.status === 'interrupted' && <p className="text-xs text-muted">This conversation was interrupted (the server restarted). Send another message to continue.</p>}
      <div ref={end} />
    </div>
  )
}

function Approval({ workspaceId, session }: { workspaceId: string; session: AgentSession }) {
  const approve = useApproveCommand(workspaceId, session.id)
  const p = session.pending
  if (!p) return null
  return (
    <section aria-label="Approval needed" className="mx-4 mb-2 rounded-card border border-warn-fg/50 bg-warn-bg p-3 text-warn-fg">
      <h3 className="text-[13px] font-bold">The agent wants to run a command</h3>
      <pre className="mt-1.5 overflow-x-auto whitespace-pre-wrap rounded-lg bg-bg px-2 py-1.5 font-mono text-[12px] text-fg">{p.command}</pre>
      <p className="mt-1.5 text-xs">{p.reason}</p>
      <div className="mt-2 flex gap-2">
        <Button size="sm" variant="primary" loading={approve.isPending} onClick={() => approve.mutate({ callId: p.callId, approve: true })}>Run it</Button>
        <Button size="sm" onClick={() => approve.mutate({ callId: p.callId, approve: false })}>Decline</Button>
      </div>
    </section>
  )
}

/** The chat panel: conversations, tool activity, approvals and the composer (PRD F17). */
export function ChatView({ workspaceId, onReview, focusSession }: { workspaceId: string; onReview: (setId: string) => void; focusSession?: string | null }) {
  const { data: sessions = [] } = useSessions(workspaceId)
  const [picked, setPicked] = useState<string | null>(null)
  const current = picked ?? focusSession ?? sessions[0]?.id ?? null
  const { data: session } = useSession(workspaceId, current)
  const create = useCreateSession(workspaceId)
  const send = useSendMessage(workspaceId)
  const stop = useStopSession(workspaceId, current ?? '')
  const [text, setText] = useState('')
  useEffect(() => { if (focusSession) setPicked(focusSession) }, [focusSession])

  const busy = session?.status === 'running' || session?.status === 'awaiting_approval'
  const submit = async () => {
    const t = text.trim()
    if (!t || busy) return
    let id = current
    if (!id) {
      const s = await create.mutateAsync()
      id = s.id
      setPicked(id)
    }
    setText('')
    send.mutate({ id, text: t }, { onError: () => setText(t) })
  }
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex items-center gap-2 border-b border-line px-4 py-2">
        <label htmlFor="conversation" className="sr-only">Conversation</label>
        <select id="conversation" value={current ?? ''} onChange={(e) => setPicked(e.target.value || null)} className="h-8 min-w-0 flex-1 rounded-lg border border-line-strong bg-field px-2 text-xs text-fg">
          {sessions.length === 0 && <option value="">No conversations yet</option>}
          {sessions.map((s) => <option key={s.id} value={s.id}>{s.title || 'New conversation'}</option>)}
        </select>
        <Button size="sm" loading={create.isPending} onClick={() => create.mutate(undefined, { onSuccess: (s) => setPicked(s.id) })}>New chat</Button>
      </div>
      {session ? <Messages workspaceId={workspaceId} session={session} onReview={onReview} /> : (
        <div className="flex-1 px-4 py-3 text-[13px] text-muted">Ask the agent about your code or canvas. It proposes changes for you to review; nothing is written until you accept.</div>
      )}
      {session && <Approval workspaceId={workspaceId} session={session} />}
      <form className="border-t border-line p-3" onSubmit={(e) => { e.preventDefault(); void submit() }}>
        <label htmlFor="agent-message" className="sr-only">Message to the agent</label>
        <textarea id="agent-message" rows={3} value={text} onChange={(e) => setText(e.target.value)} disabled={busy}
          onKeyDown={(e) => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) { e.preventDefault(); void submit() } }}
          placeholder="Ask about your code, or describe a change…" className="w-full resize-none rounded-lg border border-line-strong bg-field p-2 text-[13px] text-fg placeholder:text-faint" />
        <div className="mt-2 flex items-center gap-2">
          {(send.error || create.error) && <span role="alert" className="min-w-0 flex-1 text-xs text-err-fg">{(send.error ?? create.error)?.message}</span>}
          <div className="ml-auto flex gap-2">
            {busy && <Button size="sm" onClick={() => stop.mutate()}>Stop</Button>}
            <Button size="sm" variant="primary" type="submit" disabled={!text.trim() || busy} loading={send.isPending}>Send</Button>
          </div>
        </div>
      </form>
    </div>
  )
}
