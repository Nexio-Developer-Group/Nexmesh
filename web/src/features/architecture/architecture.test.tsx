import { screen, within } from '@testing-library/react'
import { ArchitectureView, type ArchView } from './ArchitectureView'
import { mockApi, renderPage } from '@/test/utils'

const view: ArchView = {
  workspace: 'shop',
  layers: [{ id: 'frontend', label: 'Frontend' }, { id: 'backend', label: 'Backend' }, { id: 'data', label: 'Data' }],
  nodes: [
    { id: 'a', catalog: 'react', name: 'React', layer: 'frontend', mode: 'installed', version: '19', state: 'ready' },
    { id: 'b', catalog: 'nestjs', name: 'NestJS', layer: 'backend', mode: 'installed', version: '10', state: 'ready', highlight: true },
    { id: 'c', catalog: 'mongodb', name: 'MongoDB', layer: 'data', mode: 'installed', version: '8', state: 'failed' },
  ],
  edges: [{ id: 'w1', from: 'b', to: 'c', name: 'mongodb', state: 'unreachable', variables: 5 }],
  drift: [{ kind: 'unreachable', nodeId: 'c', message: 'MongoDB is failed. port in use' }, { kind: 'isolated', nodeId: 'a', message: 'React has no connections.' }],
}

describe('ArchitectureView', () => {
  it('shows layers, connections with a text state, drift and export links', async () => {
    mockApi({ 'GET /api/workspaces/ws/architecture': () => view })
    renderPage(<ArchitectureView workspaceId="ws" />)
    expect(await screen.findByRole('heading', { name: 'Architecture' })).toBeInTheDocument()
    const layers = screen.getByRole('region', { name: 'Components by layer' })
    expect(within(layers).getByText('Frontend')).toBeInTheDocument()
    expect(within(layers).getByText('NestJS')).toHaveClass('font-bold') // highlighted
    const conns = screen.getByRole('region', { name: 'Connections' })
    expect(within(conns).getByText('NestJS → MongoDB')).toBeInTheDocument()
    expect(within(conns).getByText('Unreachable')).toBeInTheDocument()
    const drift = screen.getByRole('region', { name: 'Needs attention' })
    expect(within(drift).getByText(/MongoDB is failed/)).toBeInTheDocument()
    expect(within(drift).getByText(/React has no connections/)).toBeInTheDocument()
    expect(screen.getByRole('img')).toHaveAttribute('alt', expect.stringContaining('3 components, 1 connections'))
    const svgLink = screen.getByRole('link', { name: 'Image (SVG)' })
    expect(svgLink).toHaveAttribute('href', expect.stringContaining('format=svg&download=1'))
    expect(screen.getByRole('link', { name: 'Mermaid' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Markdown summary' })).toBeInTheDocument()
  })

  it('says so when nothing is wrong and when the canvas is empty', async () => {
    mockApi({ 'GET /api/workspaces/ws/architecture': () => ({ ...view, drift: [] }) })
    const { unmount } = renderPage(<ArchitectureView workspaceId="ws" />)
    expect(await screen.findByText(/Everything configured is reachable/)).toBeInTheDocument()
    unmount()
    mockApi({ 'GET /api/workspaces/ws/architecture': () => ({ workspace: 'shop', layers: [], nodes: [], edges: [], drift: [] }) })
    renderPage(<ArchitectureView workspaceId="ws" />)
    expect(await screen.findByText(/Add cards to the canvas/)).toBeInTheDocument()
  })

  it('passes a change set highlight to the server', async () => {
    const calls = mockApi({ 'GET /api/workspaces/ws/architecture': () => view })
    renderPage(<ArchitectureView workspaceId="ws" highlight={['b', 'c']} />)
    await screen.findByRole('heading', { name: 'Architecture' })
    expect(calls[0].url).toContain('highlight=b,c')
  })
})
