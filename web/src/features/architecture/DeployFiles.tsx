import { useMutation, useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { api } from '@/api/client'
import { Button } from '@/components/ui/Button'

interface PlanFile { path: string; content: string; exists: boolean; ours: boolean }
interface Plan { files: PlanFile[]; notes: string[] }
interface WriteResult { written: string[]; skipped: string[] }

const status = (f: PlanFile) => (!f.exists ? 'New' : f.ours ? 'Exists (generated earlier)' : 'Exists (yours)')

/**
 * Dockerfile and docker-compose export (PRD F25). Shows what would be written,
 * and writes it to the workspace only when asked. Files that already exist are
 * kept unless replacing them is chosen. Nothing here contains a secret value.
 */
export function DeployFiles({ workspaceId }: { workspaceId: string }) {
  const plan = useQuery({ queryKey: ['deploy-plan', workspaceId], queryFn: () => api.get<Plan>(`/api/workspaces/${workspaceId}/export/compose`), refetchOnWindowFocus: false })
  const [picked, setPicked] = useState<Record<string, boolean>>({})
  const [overwrite, setOverwrite] = useState(false)
  const write = useMutation({
    mutationFn: (paths: string[]) => api.post<WriteResult>(`/api/workspaces/${workspaceId}/export/compose/write`, { paths, overwrite, confirm: true }),
    onSuccess: () => plan.refetch(),
  })
  const files = plan.data?.files ?? []
  const chosen = files.filter((f) => picked[f.path] ?? !f.exists).map((f) => f.path)
  const anyExisting = files.some((f) => f.exists && chosen.includes(f.path))

  return (
    <section aria-label="Docker files" className="mt-6">
      <h2 className="text-[13px] font-bold">Docker files</h2>
      <p className="mt-0.5 text-xs text-muted">
        A docker-compose.yml for the installed services, Dockerfiles for the applications found in the workspace, and a .env.example listing variable names.
        Connected services stay outside as references. No secret values are written.
      </p>
      {plan.isPending && <p className="mt-2 text-sm text-muted">Reading the workspace…</p>}
      {plan.error && <p role="alert" className="mt-2 rounded-lg bg-warn-bg px-3 py-2 text-[13px] text-warn-fg">{plan.error.message}</p>}
      {plan.data && (
        <>
          {plan.data.notes.length > 0 && (
            <ul className="mt-2 space-y-1 text-[13px] text-muted">{plan.data.notes.map((n) => <li key={n}>• {n}</li>)}</ul>
          )}
          {files.length > 0 && (
            <ul className="mt-3 space-y-2">
              {files.map((f) => (
                <li key={f.path} className="rounded-[10px] border border-line bg-panel p-3">
                  <div className="flex flex-wrap items-center gap-3">
                    <label className="flex items-center gap-2 text-[13px] font-semibold">
                      <input type="checkbox" checked={picked[f.path] ?? !f.exists} onChange={(e) => setPicked((p) => ({ ...p, [f.path]: e.target.checked }))} />
                      <span className="font-mono">{f.path}</span>
                    </label>
                    <span className="rounded bg-neutral-bg px-1.5 py-0.5 text-[11px] font-semibold text-neutral-fg">{status(f)}</span>
                  </div>
                  <details className="mt-2">
                    <summary className="cursor-pointer text-xs text-link">Preview</summary>
                    <pre className="mt-2 max-h-64 overflow-auto rounded-lg bg-field p-3 font-mono text-[11.5px] leading-relaxed text-fg">{f.content}</pre>
                  </details>
                </li>
              ))}
            </ul>
          )}
          {files.length > 0 && (
            <div className="mt-3 flex flex-wrap items-center gap-4">
              <Button variant="primary" disabled={chosen.length === 0} loading={write.isPending} onClick={() => write.mutate(chosen)}>
                Write {chosen.length} file{chosen.length === 1 ? '' : 's'} to the workspace
              </Button>
              {anyExisting && (
                <label className="flex items-center gap-2 text-[13px]">
                  <input type="checkbox" checked={overwrite} onChange={(e) => setOverwrite(e.target.checked)} /> Replace files that already exist
                </label>
              )}
            </div>
          )}
          {write.error && <p role="alert" className="mt-2 text-sm text-err-fg">{write.error.message}</p>}
          {write.data && (
            <p role="status" className="mt-2 rounded-lg bg-ok-bg px-3 py-2 text-[13px] text-ok-fg">
              Wrote {write.data.written.length} file{write.data.written.length === 1 ? '' : 's'}
              {write.data.skipped.length > 0 && `; kept ${write.data.skipped.length} that already existed (${write.data.skipped.join(', ')})`}.
              Review them in the Code tab before committing.
            </p>
          )}
        </>
      )}
    </section>
  )
}
