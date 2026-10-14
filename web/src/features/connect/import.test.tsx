import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ImportConnectionDialog } from './ImportConnectionDialog'
import { usePrefill } from './prefill'
import { mockApi, renderPage } from '@/test/utils'
import type { CatalogComponent } from '@/api/types'

const defs = new Map([['mongodb', { id: 'mongodb', name: 'MongoDB', monogram: 'Mo' }], ['redis', { id: 'redis', name: 'Redis', monogram: 'Rd' }]] as unknown as [string, CatalogComponent][])
const found = {
  format: 'compose',
  candidates: [
    { source: 'db', catalogId: 'mongodb', name: 'db', fields: { username: 'root', password: 'devpassword', port: 27018 }, secrets: ['password'], missing: ['host', 'database'], notes: ['The host is not known from a compose file.'] },
    { source: 'cache', catalogId: 'redis', name: 'cache', fields: { port: 6379 }, secrets: [], missing: ['host'] },
  ],
  skipped: [{ source: 'web', reason: 'Not a supported service (image nginx:alpine).' }],
}

describe('ImportConnectionDialog', () => {
  it('finds connections, hides secret values, flags gaps, and hands the choice back', async () => {
    const onPick = vi.fn()
    const calls = mockApi({ 'POST /api/connections/import': () => found })
    renderPage(<ImportConnectionDialog defs={defs} onPick={onPick} onClose={() => {}} />)
    const find = screen.getByRole('button', { name: 'Find connections' })
    expect(find).toBeDisabled()
    await userEvent.type(screen.getByLabelText(/Paste a connection string/), 'services:')
    await userEvent.click(find)
    expect(await screen.findByText(/Found 2 connections in the docker-compose file/)).toBeInTheDocument()
    const row = screen.getByText('MongoDB').closest('li')!
    expect(row.textContent).toContain('password=••••')
    expect(row.textContent).not.toContain('devpassword')
    expect(within(row).getByText(/enter it yourself: host, database/)).toBeInTheDocument()
    expect(within(row).getByText(/host is not known from a compose file/)).toBeInTheDocument()
    expect(screen.getByText(/1 item skipped/)).toBeInTheDocument()
    await userEvent.click(within(row).getByRole('button', { name: 'Use db' }))
    expect(onPick).toHaveBeenCalledWith(found.candidates[0])
    expect(calls[0].body).toEqual({ content: 'services:', catalogId: undefined })
  })

  it('restricts to one technology and shows the server message for unrecognised input', async () => {
    const calls = mockApi({ 'POST /api/connections/import': () => ({ status: 422, body: { error: { code: 'unrecognized_input', message: 'No supported connection for Redis was found. Supported: MongoDB, MySQL or MariaDB, Redis, RabbitMQ and Kafka.' } } }) })
    renderPage(<ImportConnectionDialog catalogId="redis" defs={defs} onPick={() => {}} onClose={() => {}} />)
    await userEvent.type(screen.getByLabelText(/Paste a connection string/), 'FOO=bar')
    await userEvent.click(screen.getByRole('button', { name: 'Find connections' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('No supported connection for Redis was found')
    await waitFor(() => expect(calls[0].body).toEqual({ content: 'FOO=bar', catalogId: 'redis' }))
    expect(screen.getByText(/Nothing is saved/)).toBeInTheDocument()
  })
})

describe('prefill store', () => {
  it('hands values to a card exactly once', () => {
    usePrefill.getState().set('c1', { connName: 'db', fields: { host: 'h' }, secrets: { password: 'pw' } })
    expect(usePrefill.getState().take('c1')?.secrets.password).toBe('pw')
    expect(usePrefill.getState().take('c1')).toBeUndefined()
    expect(JSON.stringify(localStorage) + JSON.stringify(sessionStorage)).not.toContain('pw') // memory only
  })
})
