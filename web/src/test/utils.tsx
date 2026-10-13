import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render } from '@testing-library/react'
import type { ReactElement } from 'react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'

/** Renders a page inside router + query providers. */
export function renderPage(ui: ReactElement, { path = '/', route = '/' }: { path?: string; route?: string } = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[route]}>
        <Routes>
          <Route path={path} element={ui} />
          <Route path="*" element={<div>elsewhere</div>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  )
}

type Handler = (url: string, init?: RequestInit) => unknown | Promise<unknown>

/** Stubs fetch with route handlers keyed by "METHOD /path-prefix". */
export function mockApi(routes: Record<string, Handler>) {
  const calls: { method: string; url: string; body?: unknown }[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
    const method = init?.method ?? 'GET'
    calls.push({ method, url, body: init?.body ? JSON.parse(String(init.body)) : undefined })
    const key = Object.keys(routes).find((k) => {
      const [m, p] = k.split(' ')
      return m === method && url.startsWith(p)
    })
    if (!key) return new Response(JSON.stringify({ error: { code: 'not_mocked', message: `no mock for ${method} ${url}` } }), { status: 500 })
    const out = (await routes[key](url, init)) as { status?: number; body?: unknown } | unknown
    if (out && typeof out === 'object' && 'status' in out) {
      const o = out as { status: number; body?: unknown }
      return new Response(o.body === undefined ? null : JSON.stringify(o.body), { status: o.status })
    }
    return new Response(JSON.stringify(out), { status: 200 })
  }))
  return calls
}
