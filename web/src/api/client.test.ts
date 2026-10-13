import { api, ApiError } from './client'

afterEach(() => vi.restoreAllMocks())

describe('api client', () => {
  it('maps validation errors with field and code', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(
      JSON.stringify({ error: { code: 'cpu_out_of_range', message: 'CPU must be between 1 and 12 vCPU on this host.', field: 'cpu' } }),
      { status: 422 })))
    await expect(api.post('/api/workspaces', {})).rejects.toMatchObject({ status: 422, code: 'cpu_out_of_range', field: 'cpu' })
  })

  it('reports a network failure as a friendly error', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('failed')))
    const err = (await api.get('/api/host').catch((e) => e)) as ApiError
    expect(err).toBeInstanceOf(ApiError)
    expect(err.code).toBe('network')
  })

  it('returns undefined for 204', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 204 })))
    await expect(api.del('/api/workspaces/x')).resolves.toBeUndefined()
  })
})
