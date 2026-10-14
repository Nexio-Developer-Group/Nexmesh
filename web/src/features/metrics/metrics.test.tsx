import { screen, within } from '@testing-library/react'
import { CardMetrics, WorkspaceUsage } from './CardMetrics'
import { mockApi, renderPage } from '@/test/utils'
import type { MetricsSnapshot, MetricSample } from './hooks'

const s = (o: Partial<MetricSample> = {}): MetricSample => ({ at: '2026-10-14T10:00:00Z', cpuPercent: 4.2, memUsedMb: 120, memLimitMb: 256, diskUsedMb: 512, ...o })
const snap: MetricsSnapshot = {
  at: '', limits: { cpu: 2, ramGb: 4, storageGb: 20 }, simulated: true,
  workspace: { id: 'ws', catalogId: '', name: 'Workspace', mode: 'installed', current: s({ memUsedMb: 1536, diskUsedMb: 2048 }), history: [s({ cpuPercent: 1 }), s({ cpuPercent: 5 })] },
  components: [
    { id: 'c1', catalogId: 'redis', name: 'Redis', mode: 'installed', current: s({ simulated: true }), history: [s({ cpuPercent: 2 }), s({ cpuPercent: 4.2 })] },
    { id: 'c2', catalogId: 'mysql', name: 'MySQL', mode: 'installed', history: [], unavailable: 'Not running, so there is nothing to measure.' },
    { id: 'c3', catalogId: 'redis', name: 'Redis (cache)', mode: 'connected', current: s({ latencyMs: 41 }), history: [s({ latencyMs: 20 }), s({ latencyMs: 41 })] },
    { id: 'c4', catalogId: 'mongodb', name: 'MongoDB', mode: 'installed', current: s({ diskUsedMb: -1 }), history: [] },
  ],
}

describe('CardMetrics', () => {
  it('shows CPU, memory and storage for an installed card, and says when numbers are simulated', async () => {
    mockApi({ 'GET /api/workspaces/ws/metrics': () => snap })
    renderPage(<CardMetrics workspaceId="ws" componentId="c1" />)
    expect(await screen.findByText('4.2%')).toBeInTheDocument()
    expect(screen.getByText('120 MB of 256 MB')).toBeInTheDocument()
    expect(screen.getByRole('progressbar', { name: 'Memory used' })).toHaveAttribute('aria-valuenow', '47')
    expect(screen.getByRole('img', { name: /CPU over the last 2 readings: now 4.2%/ })).toBeInTheDocument()
    expect(screen.getByText('512 MB')).toBeInTheDocument()
    expect(screen.getByText(/generated, not measured/)).toBeInTheDocument()
  })

  it('explains a stopped service and an unmeasurable disk', async () => {
    mockApi({ 'GET /api/workspaces/ws/metrics': () => snap })
    const { unmount } = renderPage(<CardMetrics workspaceId="ws" componentId="c2" />)
    expect(await screen.findByText('Not running, so there is nothing to measure.')).toBeInTheDocument()
    unmount()
    renderPage(<CardMetrics workspaceId="ws" componentId="c4" />)
    expect(await screen.findByText('not measurable')).toBeInTheDocument()
  })

  it('shows latency from health checks for a connected card', async () => {
    mockApi({ 'GET /api/workspaces/ws/metrics': () => snap })
    renderPage(<CardMetrics workspaceId="ws" componentId="c3" />)
    expect(await screen.findByText('41 ms')).toBeInTheDocument()
    expect(screen.getByRole('img', { name: /Latency over the last 2 readings: now 41 ms/ })).toBeInTheDocument()
    expect(screen.queryByText('CPU')).toBeNull()
  })
})

describe('WorkspaceUsage', () => {
  it('shows the workspace against its limits', async () => {
    mockApi({ 'GET /api/workspaces/ws/metrics': () => snap })
    renderPage(<WorkspaceUsage workspaceId="ws" limits={{ cpu: 2, ramGb: 4, storageGb: 20 }} />)
    const region = await screen.findByRole('region', { name: 'Current usage' })
    expect(within(region).getByText('4.2% of 2 vCPU')).toBeInTheDocument()
    expect(within(region).getByText('1.5 GB of 4 GB')).toBeInTheDocument()
    expect(within(region).getByText('2.0 GB of 20 GB')).toBeInTheDocument()
  })
})
