import { useState } from 'react'
import { ApiError } from '@/api/client'
import { Button } from '@/components/ui/Button'
import { Field } from '@/components/ui/Field'
import { Modal } from '@/components/ui/Modal'
import { useCredentialActions, useCredentials, type GitCredential } from './hooks'

const PROVIDERS = [['github', 'GitHub'], ['gitlab', 'GitLab'], ['bitbucket', 'Bitbucket'], ['custom', 'Other host']] as const

export function GitCredentialsTab({ workspaceId }: { workspaceId: string }) {
  const { data, isPending, error } = useCredentials(workspaceId)
  const acts = useCredentialActions(workspaceId)
  const [adding, setAdding] = useState(false)
  const [replacing, setReplacing] = useState<GitCredential | null>(null)
  const [revoking, setRevoking] = useState<GitCredential | null>(null)
  return (
    <section aria-label="Git credentials" className="max-w-3xl space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-2xl font-extrabold tracking-tight">Git credentials</h2>
          <p className="mt-1 max-w-xl text-muted">Access tokens let this workspace clone, pull and push private repositories over HTTPS. Tokens are encrypted, can't be read back, and are used only while a Git command runs. They are never written to the workspace's disk.</p>
        </div>
        <Button variant="primary" size="sm" onClick={() => setAdding(true)}>Add credential</Button>
      </div>
      {error && <p role="alert" className="rounded-lg bg-err-bg p-3 text-sm text-err-fg">{error.message}</p>}
      {isPending && <p className="text-muted">Loading…</p>}
      {data && data.length === 0 && <p className="rounded-card border border-dashed border-line-strong p-6 text-center text-sm text-muted">No credentials yet. Public repositories work without one.</p>}
      {data && data.length > 0 && (
        <ul className="divide-y divide-line overflow-hidden rounded-card border border-line bg-panel">
          {data.map((c) => (
            <li key={c.id} className="flex flex-wrap items-center gap-3 px-4 py-3">
              <div className="min-w-0 flex-1">
                <p className="truncate font-semibold">{c.label}</p>
                <p className="font-mono text-xs text-muted">{c.host} · {c.username} · token ••••••••</p>
              </div>
              <Button size="sm" variant="ghost" aria-label={`Replace token for ${c.label}`} onClick={() => setReplacing(c)}>Replace token</Button>
              <Button size="sm" variant="ghost" aria-label={`Revoke ${c.label}`} onClick={() => setRevoking(c)}>Revoke</Button>
            </li>
          ))}
        </ul>
      )}
      {adding && <AddDialog workspaceId={workspaceId} onClose={() => setAdding(false)} />}
      {replacing && <ReplaceDialog cred={replacing} onClose={() => setReplacing(null)} workspaceId={workspaceId} />}
      {revoking && (
        <Modal title={`Revoke ${revoking.label}?`} onClose={() => setRevoking(null)}
          footer={<><Button onClick={() => setRevoking(null)}>Cancel</Button>
            <Button variant="danger" loading={acts.revoke.isPending} onClick={() => acts.revoke.mutate(revoking.id, { onSuccess: () => setRevoking(null) })}>Revoke and delete token</Button></>}>
          The stored token is deleted from Nexmesh. Git commands for {revoking.host} stop authenticating immediately. Also revoke the token at the provider to be safe.
        </Modal>
      )}
    </section>
  )
}

function AddDialog({ workspaceId, onClose }: { workspaceId: string; onClose: () => void }) {
  const { add } = useCredentialActions(workspaceId)
  const [provider, setProvider] = useState<string>('github')
  const [host, setHost] = useState('')
  const [username, setUsername] = useState('')
  const [token, setToken] = useState('')
  const [label, setLabel] = useState('')
  const err = add.error instanceof ApiError ? add.error : null
  const needsHost = provider === 'custom'
  const needsUser = provider === 'bitbucket' || provider === 'custom'
  return (
    <Modal title="Add Git credential" onClose={onClose} width={520}
      footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" type="submit" form="cred-form" disabled={!token || (needsHost && !host) || (needsUser && !username)} loading={add.isPending}>Save credential</Button></>}>
      <form id="cred-form" className="space-y-4 text-fg" onSubmit={(e) => { e.preventDefault(); add.mutate({ provider, host, username, token, label }, { onSuccess: onClose }) }}>
        <div>
          <label htmlFor="cred-provider" className="mb-1.5 block text-[13px] font-semibold">Provider</label>
          <select id="cred-provider" value={provider} onChange={(e) => setProvider(e.target.value)} className="h-10 w-full rounded-[10px] border border-line-strong bg-field px-3 text-sm">
            {PROVIDERS.map(([v, l]) => <option key={v} value={v}>{l}</option>)}
          </select>
        </div>
        {needsHost && <Field label="Host" mono value={host} onChange={(e) => setHost(e.target.value)} placeholder="git.example.com" error={err?.field === 'host' ? err.message : undefined} />}
        {needsUser && <Field label="Username" mono value={username} onChange={(e) => setUsername(e.target.value)} error={err?.field === 'username' ? err.message : undefined} />}
        <Field label="Access token" type="password" autoComplete="off" mono value={token} onChange={(e) => setToken(e.target.value)}
          hint="Create a personal access token with read/write access to the repositories you need. It will not be shown again." error={err?.field === 'token' ? err.message : undefined} />
        <Field label="Label" optional value={label} onChange={(e) => setLabel(e.target.value)} />
        {err && !err.field && <p role="alert" className="rounded-lg bg-err-bg p-3 text-sm text-err-fg">{err.message}</p>}
        {err?.field === 'host' && !needsHost && <p role="alert" className="text-sm text-err-fg">{err.message}</p>}
      </form>
    </Modal>
  )
}

function ReplaceDialog({ cred, workspaceId, onClose }: { cred: GitCredential; workspaceId: string; onClose: () => void }) {
  const { update } = useCredentialActions(workspaceId)
  const [token, setToken] = useState('')
  const err = update.error instanceof ApiError ? update.error : null
  return (
    <Modal title={`Replace token for ${cred.label}`} onClose={onClose}
      footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" type="submit" form="replace-form" disabled={!token} loading={update.isPending}>Replace token</Button></>}>
      <form id="replace-form" className="text-fg" onSubmit={(e) => { e.preventDefault(); update.mutate({ id: cred.id, token }, { onSuccess: onClose }) }}>
        <Field label="New access token" type="password" autoComplete="off" mono value={token} onChange={(e) => setToken(e.target.value)} error={err?.message} />
      </form>
    </Modal>
  )
}
