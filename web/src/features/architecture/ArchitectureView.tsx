import { useQuery } from '@tanstack/react-query'
import { api } from '@/api/client'
import { WireStateBadge } from '@/features/wires/WireDialog'
import type { WireState } from '@/api/types'

export interface ArchNode { id: string; catalog: string; name: string; layer: string; mode: string; version?: string; state: string; environment?: string; highlight?: boolean }
export interface ArchEdge { id: string; from: string; to: string; name: string; state: WireState; environment?: string; variables: number }
export interface ArchDrift { kind: 'unreachable' | 'isolated' | 'dangling'; nodeId?: string; edgeId?: string; message: string }
export interface ArchView { workspace: string; layers: { id: string; label: string }[]; nodes: ArchNode[]; edges: ArchEdge[]; drift: ArchDrift[] }

export const archKeys = { view: (ws: string) => ['architecture', ws] as const }

export function useArchitecture(ws: string, highlight?: string[]) {
  const q = highlight?.length ? `?highlight=${highlight.join(',')}` : ''
  return useQuery({
    queryKey: [...archKeys.view(ws), q],
    queryFn: () => api.get<ArchView>(`/api/workspaces/${ws}/architecture${q}`),
    refetchInterval: 10_000,
  })
}

const EXPORTS = [
  { fmt: 'svg', label: 'Image (SVG)' },
  { fmt: 'mermaid', label: 'Mermaid' },
  { fmt: 'markdown', label: 'Markdown summary' },
] as const

/**
 * The architecture view (PRD F2): derived from the canvas on every load, so it
 * cannot disagree with it. The image is a convenience; the lists below carry
 * the same information as text.
 */
export function ArchitectureView({ workspaceId, highlight }: { workspaceId: string; highlight?: string[] }) {
  const { data: v, isPending, error } = useArchitecture(workspaceId, highlight)
  const q = highlight?.length ? `&highlight=${highlight.join(',')}` : ''
  if (isPending) return <p className="p-6 text-sm text-muted">Building the view…</p>
  if (error || !v) return <p role="alert" className="p-6 text-sm text-err-fg">{error?.message ?? 'Could not build the architecture view.'}</p>
  const byId = new Map(v.nodes.map((n) => [n.id, n]))
  // The query string changes whenever the shape does, so the image is never stale.
  const stamp = `${v.nodes.map((n) => n.id + n.state).join('')}${v.edges.map((e) => e.id + e.state).join('')}`.length + ':' + v.nodes.length + ':' + v.edges.length + ':' + v.drift.length
  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-y-auto p-6">
      <div className="flex flex-wrap items-center gap-3">
        <h1 className="text-lg font-extrabold">Architecture</h1>
        <p className="text-sm text-muted">Generated from the canvas. Contains no secret values.</p>
        <div className="ml-auto flex flex-wrap gap-2" role="group" aria-label="Export">
          {EXPORTS.map((e) => (
            <a key={e.fmt} href={`/api/workspaces/${workspaceId}/architecture/export?format=${e.fmt}&download=1${q}`} download
              className="inline-flex h-8 items-center rounded-[10px] border border-line-strong bg-raised px-3 text-xs font-semibold text-fg hover:border-blue">{e.label}</a>
          ))}
        </div>
      </div>

      {v.nodes.length === 0 ? (
        <p className="mt-8 text-sm text-muted">Add cards to the canvas and connect them to see the architecture.</p>
      ) : (
        <>
          <div className="mt-4 overflow-auto rounded-card border border-line bg-bg p-2">
            <img alt={`Architecture diagram of ${v.workspace}: ${v.nodes.length} components, ${v.edges.length} connections`}
              src={`/api/workspaces/${workspaceId}/architecture/export?format=svg&v=${stamp}${q}`} className="max-w-none" />
          </div>

          <section aria-label="Needs attention" className="mt-5">
            <h2 className="text-[13px] font-bold">Needs attention</h2>
            {v.drift.length === 0 ? (
              <p className="mt-1 text-[13px] text-muted">Everything configured is reachable and connected.</p>
            ) : (
              <ul className="mt-2 space-y-1.5">
                {v.drift.map((d, i) => (
                  <li key={i} className="flex items-start gap-2 rounded-lg bg-warn-bg px-3 py-2 text-[13px] text-warn-fg">
                    <span aria-hidden>▲</span>
                    <span><strong className="capitalize">{d.kind}:</strong> {d.message}</span>
                  </li>
                ))}
              </ul>
            )}
          </section>

          <section aria-label="Components by layer" className="mt-5 grid gap-4 md:grid-cols-2 xl:grid-cols-3">
            {v.layers.map((l) => {
              const ns = v.nodes.filter((n) => n.layer === l.id)
              if (!ns.length) return null
              return (
                <div key={l.id} className="rounded-card border border-line bg-panel p-4">
                  <h2 className="text-[13px] font-bold">{l.label}</h2>
                  <ul className="mt-2 space-y-1.5 text-[13px]">
                    {ns.map((n) => (
                      <li key={n.id} className="flex items-center justify-between gap-2">
                        <span className={n.highlight ? 'font-bold text-blue' : ''}>{n.name}{n.version && <span className="ml-1.5 font-mono text-[11px] text-muted">{n.version}</span>}</span>
                        <span className="text-xs capitalize text-muted">{n.mode} · {n.state.replace('_', ' ')}</span>
                      </li>
                    ))}
                  </ul>
                </div>
              )
            })}
          </section>

          {v.edges.length > 0 && (
            <section aria-label="Connections" className="mt-5">
              <h2 className="text-[13px] font-bold">Connections</h2>
              <ul className="mt-2 space-y-1.5 text-[13px]">
                {v.edges.map((e) => (
                  <li key={e.id} className="flex items-center gap-3">
                    <span>{byId.get(e.from)?.name} → {byId.get(e.to)?.name}</span>
                    <span className="font-mono text-[11px] text-muted">{e.name}</span>
                    <WireStateBadge state={e.state} />
                  </li>
                ))}
              </ul>
            </section>
          )}
        </>
      )}
    </div>
  )
}
