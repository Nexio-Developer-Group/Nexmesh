import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ConnectTab } from './ConnectTab'
import { mockApi, renderPage } from '@/test/utils'
import type { CanvasComponent, CatalogComponent } from '@/api/types'

const api: CatalogComponent = {
  id: 'externalapi', name: 'External API', monogram: 'Ap', categories: ['external'], kind: 'service', description: '', supportedModes: ['connect'],
  versions: { default: 'any', supported: ['any'] },
  connection: {
    schemes: [], defaultPort: 443, envPrefix: 'API', fields: [
      { name: 'baseUrl', label: 'Base URL', type: 'string', required: true, advanced: false },
      { name: 'authType', label: 'Authentication', type: 'select', required: true, advanced: false, default: 'none', options: ['none', 'apiKey', 'bearer', 'basic'] },
      { name: 'apiKeyHeader', label: 'API key header', type: 'string', required: false, advanced: false, default: 'X-API-Key', showWhen: { field: 'authType', in: ['apiKey'] } },
      { name: 'apiKey', label: 'API key', type: 'password', required: false, advanced: false, showWhen: { field: 'authType', in: ['apiKey'] } },
      { name: 'token', label: 'Bearer token', type: 'password', required: false, advanced: false, showWhen: { field: 'authType', in: ['bearer'] } },
      { name: 'username', label: 'Username', type: 'string', required: false, advanced: false, showWhen: { field: 'authType', in: ['basic'] } },
      { name: 'password', label: 'Password', type: 'password', required: false, advanced: false, showWhen: { field: 'authType', in: ['basic'] } },
    ],
  },
}
const card: CanvasComponent = { id: 'c1', workspaceId: 'w1', catalogId: 'externalapi', mode: '', version: 'any', settings: {}, desiredState: 'draft', observedState: 'draft', progress: 0, x: 0, y: 0 }

describe('External API connect form', () => {
  it('shows only the credential that goes with the chosen authentication type', async () => {
    renderPage(<ConnectTab workspaceId="w1" component={card} def={api} />)
    expect(screen.queryByLabelText(/^API key optional$/)).toBeNull()
    expect(screen.queryByLabelText(/^Bearer token/)).toBeNull()
    expect(screen.queryByLabelText(/^Password/)).toBeNull()
    const auth = screen.getByLabelText(/^Authentication/)
    await userEvent.selectOptions(auth, 'apiKey')
    expect(screen.getByLabelText(/^API key optional$/)).toBeInTheDocument()
    expect(screen.getByLabelText(/^API key header/)).toBeInTheDocument()
    await userEvent.selectOptions(auth, 'bearer')
    expect(screen.queryByLabelText(/^API key optional$/)).toBeNull()
    expect(screen.getByLabelText(/^Bearer token/)).toBeInTheDocument()
    await userEvent.selectOptions(auth, 'basic')
    expect(screen.getByLabelText(/^Username/)).toBeInTheDocument()
    expect(screen.getByLabelText(/^Password/)).toBeInTheDocument()
    // No connection string box for a technology without schemes.
    expect(screen.queryByLabelText(/Paste connection string/)).toBeNull()
  })

  it('never sends a credential typed for an authentication type that is no longer selected', async () => {
    const calls = mockApi({ 'POST /api/workspaces/w1/components/c1/connection/test': () => ({ passed: true, at: '2026-10-13T12:00:00Z', stages: [] }) })
    renderPage(<ConnectTab workspaceId="w1" component={card} def={api} />)
    await userEvent.type(screen.getByLabelText('Connection name'), 'billing')
    await userEvent.type(screen.getByLabelText(/^Base URL/), 'https://api.example.com')
    const auth = screen.getByLabelText(/^Authentication/)
    await userEvent.selectOptions(auth, 'apiKey')
    await userEvent.type(screen.getByLabelText(/^API key optional$/), 'sk_typed_then_abandoned')
    await userEvent.selectOptions(auth, 'bearer')
    await userEvent.type(screen.getByLabelText(/^Bearer token/), 'tok_real')
    await userEvent.click(screen.getByRole('button', { name: 'Test Connection' }))
    await waitFor(() => expect(calls.some((c) => c.url.endsWith('/test'))).toBe(true))
    const sent = calls.find((c) => c.url.endsWith('/test'))!.body as { fields: Record<string, unknown> }
    expect(sent.fields.token).toBe('tok_real')
    expect(sent.fields.authType).toBe('bearer')
    expect(JSON.stringify(sent)).not.toContain('sk_typed_then_abandoned')
  })
})
