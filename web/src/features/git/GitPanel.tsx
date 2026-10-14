import clsx from 'clsx'
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { ApiError } from '@/api/client'
import { Button } from '@/components/ui/Button'
import { Field } from '@/components/ui/Field'
import { Modal } from '@/components/ui/Modal'
import { useBranches, useDiff, useGitActions, useGitLog, useGitStatus, useRepos, type FileChange, type GitStatus } from './hooks'

function ErrorNote({ error, workspaceId }: { error: unknown; workspaceId: string }) {
  if (!(error instanceof ApiError)) return null
  return (
    <div role="alert" className="mx-3 my-2 rounded-lg bg-err-bg p-2.5 text-xs text-err-fg">
      <p>{error.message}</p>
      {error.code === 'auth_failed' && <Link className="mt-1 inline-block font-semibold underline" to={`/w/${workspaceId}/settings?tab=git`}>Open Git credentials</Link>}
      {error.detail && (
        <details className="mt-1"><summary className="cursor-pointer">Details</summary><pre className="mt-1 whitespace-pre-wrap font-mono text-[11px]">{error.detail}</pre></details>
      )}
    </div>
  )
}

/** Source-control panel for one repository at a time. */
export function GitPanel({ workspaceId, onOpenFile }: { workspaceId: string; onOpenFile: (repo: string, path: string) => void }) {
  const repos = useRepos(workspaceId)
  const [repo, setRepo] = useState<string | null>(null)
  const [cloning, setCloning] = useState(false)
  useEffect(() => { if (repo === null && repos.data?.length) setRepo(repos.data[0]) }, [repos.data, repo])

  if (repos.isPending) return <p role="status" className="p-4 text-sm text-muted">Looking for repositories…</p>
  if (repos.error) return <ErrorNote error={repos.error} workspaceId={workspaceId} />
  return (
    <div className="flex h-full min-h-0 flex-col" aria-label="Source control">
      <div className="flex items-center gap-2 border-b border-line px-3 py-2">
        {repos.data!.length > 0 && (
          <select aria-label="Repository" value={repo ?? ''} onChange={(e) => setRepo(e.target.value)} className="h-8 min-w-0 flex-1 rounded-lg border border-line-strong bg-field px-2 text-[13px]">
            {repos.data!.map((r) => <option key={r} value={r}>{r === '' ? '/ (workspace root)' : r}</option>)}
          </select>
        )}
        <Button size="sm" onClick={() => setCloning(true)}>{repos.data!.length ? 'Clone' : 'Clone a repository'}</Button>
      </div>
      {repos.data!.length === 0 && (
        <p className="p-4 text-sm text-muted">No Git repositories in this workspace yet. Clone one, or run <code className="font-mono">git init</code> in the terminal.</p>
      )}
      {repo !== null && <RepoView key={repo} workspaceId={workspaceId} repo={repo} onOpenFile={onOpenFile} />}
      {cloning && <CloneDialog workspaceId={workspaceId} onClose={() => setCloning(false)} onCloned={(r) => { setRepo(r); repos.refetch() }} />}
    </div>
  )
}

function RepoView({ workspaceId, repo, onOpenFile }: { workspaceId: string; repo: string; onOpenFile: (repo: string, path: string) => void }) {
  const status = useGitStatus(workspaceId, repo)
  const acts = useGitActions(workspaceId)
  const [message, setMessage] = useState('')
  const [branchesOpen, setBranchesOpen] = useState(false)
  const [diff, setDiff] = useState<{ f: FileChange; staged: boolean } | null>(null)
  const [discard, setDiscard] = useState<FileChange | null>(null)
  const [showLog, setShowLog] = useState(false)
  const log = useGitLog(workspaceId, repo, showLog)
  const st = status.data

  const lastError = [acts.stage, acts.discard, acts.commit, acts.switchBranch, acts.fetch, acts.pull, acts.push].map((m) => m.error).find(Boolean) ?? status.error
  const staged = st?.files.filter((f) => f.staged) ?? []
  const changes = st?.files.filter((f) => f.unstaged || f.conflicted) ?? []
  const untracked = st?.files.filter((f) => f.untracked) ?? []

  const openPr = () => acts.prUrl.mutate(repo, { onSuccess: (r) => window.open(r.url, '_blank', 'noopener,noreferrer') })
  const sync = (op: 'fetch' | 'pull' | 'push') => acts[op].mutate({ repo })

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-y-auto">
      {st && (
        <div className="flex flex-wrap items-center gap-2 border-b border-line px-3 py-2">
          <button type="button" aria-haspopup="dialog" onClick={() => setBranchesOpen(true)} className="max-w-full truncate rounded-md border border-line-strong px-2 py-1 font-mono text-xs hover:border-blue">
            ⎇ {st.detached ? 'detached HEAD' : st.branch}
          </button>
          {(st.ahead > 0 || st.behind > 0) && <span className="font-mono text-[11px] text-muted" aria-label={`${st.ahead} ahead, ${st.behind} behind`}>↑{st.ahead} ↓{st.behind}</span>}
          <span className="ml-auto flex gap-1">
            <Button size="sm" variant="ghost" loading={acts.fetch.isPending} onClick={() => sync('fetch')}>Fetch</Button>
            <Button size="sm" variant="ghost" loading={acts.pull.isPending} onClick={() => sync('pull')}>Pull</Button>
            <Button size="sm" variant="ghost" loading={acts.push.isPending} onClick={() => sync('push')}>{st.upstream ? 'Push' : 'Publish'}</Button>
          </span>
        </div>
      )}
      <ErrorNote error={lastError} workspaceId={workspaceId} />
      {status.isPending && <p role="status" className="p-4 text-sm text-muted">Reading repository…</p>}
      {st && (
        <>
          <div className="border-b border-line p-3">
            <label htmlFor="commit-msg" className="sr-only">Commit message</label>
            <textarea id="commit-msg" value={message} onChange={(e) => setMessage(e.target.value)} rows={3} placeholder="Commit message"
              className="w-full resize-none rounded-lg border border-line-strong bg-field p-2 text-[13px] placeholder:text-faint" />
            {staged.some((f) => st.agentFiles?.includes(f.path)) && (
              <p role="note" className="mt-1.5 text-xs text-muted">Some staged files were written by the Code Agent, so this commit will include an “Assisted-by: Nexmesh Code Agent” line.</p>
            )}
            <Button size="sm" variant="primary" className="mt-2 w-full" disabled={staged.length === 0 || !message.trim()} loading={acts.commit.isPending}
              onClick={() => acts.commit.mutate({ repo, message }, { onSuccess: () => setMessage('') })}>
              Commit {staged.length > 0 ? `${staged.length} file${staged.length === 1 ? '' : 's'}` : ''}
            </Button>
          </div>
          <Section title="Staged changes" files={staged} actionLabel="Unstage" onAction={(f) => acts.stage.mutate({ repo, paths: [f.path], unstage: true })}
            onAll={staged.length ? () => acts.stage.mutate({ repo, paths: staged.map((f) => f.path), unstage: true }) : undefined} allLabel="Unstage all"
            onOpenDiff={(f) => setDiff({ f, staged: true })} onOpenFile={(f) => onOpenFile(repo, f.path)} />
          <Section title="Changes" files={changes} actionLabel="Stage" onAction={(f) => acts.stage.mutate({ repo, paths: [f.path] })}
            onAll={changes.length ? () => acts.stage.mutate({ repo, paths: changes.map((f) => f.path) }) : undefined} allLabel="Stage all"
            onDiscard={setDiscard} onOpenDiff={(f) => setDiff({ f, staged: false })} onOpenFile={(f) => onOpenFile(repo, f.path)} />
          <Section title="Untracked" files={untracked} actionLabel="Stage" onAction={(f) => acts.stage.mutate({ repo, paths: [f.path] })}
            onAll={untracked.length ? () => acts.stage.mutate({ repo, paths: untracked.map((f) => f.path) }) : undefined} allLabel="Stage all"
            onDiscard={setDiscard} onOpenFile={(f) => onOpenFile(repo, f.path)} />
          {st.clean && <p className="px-4 py-3 text-sm text-muted">No changes. Working tree clean.</p>}

          <div className="mt-auto border-t border-line p-3">
            <div className="flex flex-wrap gap-2">
              <Button size="sm" loading={acts.prUrl.isPending} onClick={openPr}>Open pull request ↗</Button>
              <Button size="sm" variant="ghost" aria-expanded={showLog} onClick={() => setShowLog((s) => !s)}>{showLog ? 'Hide history' : 'History'}</Button>
            </div>
            {acts.prUrl.error instanceof ApiError && <p role="alert" className="mt-2 text-xs text-err-fg">{acts.prUrl.error.message}</p>}
            {showLog && (
              <ol aria-label="Recent commits" className="mt-2 space-y-1.5 text-[12.5px]">
                {log.isPending && <li className="text-muted">Loading…</li>}
                {log.data?.map((c) => <li key={c.hash}><span className="font-mono text-faint">{c.short}</span> {c.subject}<span className="block text-[11px] text-muted">{c.author} · {new Date(c.date).toLocaleString()}</span></li>)}
                {log.data?.length === 0 && <li className="text-muted">No commits yet.</li>}
              </ol>
            )}
          </div>
        </>
      )}
      {branchesOpen && st && <BranchDialog workspaceId={workspaceId} repo={repo} status={st} onClose={() => setBranchesOpen(false)} />}
      {diff && <DiffDialog workspaceId={workspaceId} repo={repo} file={diff.f} staged={diff.staged} onClose={() => setDiff(null)} />}
      {discard && (
        <Modal title={`Discard changes to ${discard.path}?`} onClose={() => setDiscard(null)}
          footer={<><Button onClick={() => setDiscard(null)}>Cancel</Button>
            <Button variant="danger" loading={acts.discard.isPending} onClick={() => acts.discard.mutate({ repo, paths: [discard.path] }, { onSuccess: () => setDiscard(null) })}>
              {discard.untracked ? 'Delete file' : 'Discard changes'}</Button></>}>
          {discard.untracked ? 'This untracked file will be deleted from the workspace.' : 'Your edits to this file will be lost.'} This cannot be undone.
        </Modal>
      )}
    </div>
  )
}

const LETTER: Record<string, string> = { M: 'Modified', A: 'Added', D: 'Deleted', R: 'Renamed', C: 'Copied', U: 'Conflict', '?': 'Untracked' }

function Section({ title, files, actionLabel, onAction, onAll, allLabel, onDiscard, onOpenDiff, onOpenFile }: {
  title: string; files: FileChange[]; actionLabel: string; onAction: (f: FileChange) => void; onAll?: () => void; allLabel: string
  onDiscard?: (f: FileChange) => void; onOpenDiff?: (f: FileChange) => void; onOpenFile: (f: FileChange) => void
}) {
  if (files.length === 0) return null
  return (
    <section aria-label={title} className="border-b border-line py-2">
      <div className="flex items-center px-3 pb-1">
        <h3 className="text-[11px] font-bold uppercase tracking-wider text-faint">{title} <span className="font-mono">{files.length}</span></h3>
        {onAll && <button type="button" onClick={onAll} className="ml-auto text-[11px] font-semibold text-link hover:underline">{allLabel}</button>}
      </div>
      <ul>
        {files.map((f) => {
          const code = f.conflicted ? 'U' : f.untracked ? '?' : (f.staged ? f.index : f.worktree)
          return (
            <li key={f.path} className="group flex items-center gap-2 px-3 py-1 text-[13px] hover:bg-raised/60">
              <span title={LETTER[code] ?? code} aria-label={LETTER[code] ?? code} className={clsx('w-4 text-center font-mono text-[11px] font-bold', code === 'D' ? 'text-err-fg' : code === 'M' ? 'text-warn-fg' : code === 'U' ? 'text-err-fg' : 'text-ok-fg')}>{code}</span>
              <button type="button" className="min-w-0 flex-1 truncate text-left" title={f.path} onClick={() => (onOpenDiff && !f.untracked ? onOpenDiff(f) : onOpenFile(f))}>
                {f.path}{f.origPath && <span className="text-faint"> ← {f.origPath}</span>}
              </button>
              <span className="flex gap-1 opacity-0 group-focus-within:opacity-100 group-hover:opacity-100">
                {!f.untracked && <button type="button" aria-label={`Open ${f.path}`} className="rounded px-1 text-xs text-muted hover:bg-line" onClick={() => onOpenFile(f)}>Open</button>}
                {onDiscard && <button type="button" aria-label={`Discard ${f.path}`} className="rounded px-1 text-xs text-muted hover:bg-line" onClick={() => onDiscard(f)}>Discard</button>}
                <button type="button" aria-label={`${actionLabel} ${f.path}`} className="rounded px-1 text-xs font-semibold text-link hover:bg-line" onClick={() => onAction(f)}>{actionLabel}</button>
              </span>
            </li>
          )
        })}
      </ul>
    </section>
  )
}

function BranchDialog({ workspaceId, repo, status, onClose }: { workspaceId: string; repo: string; status: GitStatus; onClose: () => void }) {
  const branches = useBranches(workspaceId, repo, true)
  const { switchBranch } = useGitActions(workspaceId)
  const [name, setName] = useState('')
  const err = switchBranch.error instanceof ApiError ? switchBranch.error : null
  const go = (branch: string, create: boolean) => switchBranch.mutate({ repo, branch, create }, { onSuccess: onClose })
  const local = branches.data?.filter((b) => !b.remote) ?? []
  const remote = branches.data?.filter((b) => b.remote && !local.some((l) => l.name === b.name.replace(/^origin\//, ''))) ?? []
  return (
    <Modal title="Branches" onClose={onClose} footer={<Button onClick={onClose}>Close</Button>}>
      <div className="space-y-4 text-fg">
        {!status.clean && <p className="rounded-lg bg-warn-bg p-2 text-xs text-warn-fg">You have uncommitted changes. Switching may fail if they conflict.</p>}
        <ul aria-label="Local branches" className="max-h-48 overflow-y-auto">
          {local.map((b) => (
            <li key={b.name}><button type="button" disabled={b.current} onClick={() => go(b.name, false)} className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left font-mono text-[12.5px] hover:bg-raised disabled:opacity-70">
              <span aria-hidden>{b.current ? '●' : '○'}</span>{b.name}{b.current && <span className="ml-auto font-sans text-xs text-muted">current</span>}</button></li>
          ))}
          {remote.map((b) => (
            <li key={b.name}><button type="button" onClick={() => go(b.name, false)} className="flex w-full items-center gap-2 rounded px-2 py-1.5 text-left font-mono text-[12.5px] text-muted hover:bg-raised"><span aria-hidden>↓</span>{b.name}</button></li>
          ))}
          {branches.isPending && <li className="px-2 text-muted">Loading…</li>}
        </ul>
        <form onSubmit={(e) => { e.preventDefault(); if (name) go(name, true) }} className="flex items-end gap-2">
          <Field label="New branch" mono className="flex-1" value={name} onChange={(e) => setName(e.target.value)} placeholder="feature/my-change" error={err?.field === 'branch' ? err.message : undefined} />
          <Button type="submit" variant="primary" disabled={!name} loading={switchBranch.isPending}>Create</Button>
        </form>
        {err && !err.field && <p role="alert" className="text-xs text-err-fg">{err.message}</p>}
      </div>
    </Modal>
  )
}

function DiffDialog({ workspaceId, repo, file, staged, onClose }: { workspaceId: string; repo: string; file: FileChange; staged: boolean; onClose: () => void }) {
  const d = useDiff(workspaceId, repo, file.path, staged, true)
  return (
    <Modal title={`${file.path}${staged ? ' (staged)' : ''}`} onClose={onClose} width={760} footer={<Button onClick={onClose}>Close</Button>}>
      {d.isPending && <p role="status">Loading diff…</p>}
      {d.error && <p role="alert" className="text-err-fg">{d.error.message}</p>}
      {d.data && (d.data.diff === '' ? <p>No textual changes.</p> : (
        <pre aria-label="Diff" className="max-h-[60vh] overflow-auto rounded-lg border border-line bg-bg p-3 font-mono text-[12px] leading-5 text-fg">
          {d.data.diff.split('\n').map((l, i) => (
            <span key={i} className={clsx('block whitespace-pre', l.startsWith('+') && !l.startsWith('+++') && 'bg-ok-bg text-ok-fg', l.startsWith('-') && !l.startsWith('---') && 'bg-err-bg text-err-fg', l.startsWith('@@') && 'text-info-fg')}>{l || ' '}</span>
          ))}
          {d.data.truncated && <span className="block text-muted">… diff truncated</span>}
        </pre>
      ))}
    </Modal>
  )
}

function CloneDialog({ workspaceId, onClose, onCloned }: { workspaceId: string; onClose: () => void; onCloned: (repo: string) => void }) {
  const { clone } = useGitActions(workspaceId)
  const [url, setUrl] = useState('')
  const [dir, setDir] = useState('')
  const [branch, setBranch] = useState('')
  const err = clone.error instanceof ApiError ? clone.error : null
  return (
    <Modal title="Clone a repository" onClose={onClose} width={520}
      footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" type="submit" form="clone-form" disabled={!url} loading={clone.isPending}>{clone.isPending ? 'Cloning…' : 'Clone'}</Button></>}>
      <form id="clone-form" className="space-y-4 text-fg" onSubmit={(e) => { e.preventDefault(); clone.mutate({ url, dir, branch }, { onSuccess: (r) => { onCloned(r.repo); onClose() } }) }}>
        <Field label="Repository URL" mono value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://github.com/org/repo.git" error={err?.field === 'url' ? err.message : undefined}
          hint="Private repositories need a token under Workspace settings → Git credentials." />
        <Field label="Folder name" optional mono value={dir} onChange={(e) => setDir(e.target.value)} error={err?.field === 'dir' ? err.message : undefined} />
        <Field label="Branch" optional mono value={branch} onChange={(e) => setBranch(e.target.value)} error={err?.field === 'branch' ? err.message : undefined} />
        {err && !err.field && <ErrorNote error={err} workspaceId={workspaceId} />}
      </form>
    </Modal>
  )
}
