import { validateResources } from './resources'
import type { Host } from '@/api/types'

const host: Host = { maxCpu: 12, maxRamGb: 24, maxStorageGb: 200, freeRamGb: 0, gpuAvailable: false }

describe('validateResources', () => {
  it('accepts a valid combination', () => expect(validateResources({ cpu: 4, ramGb: 8, storageGb: 40, gpu: false }, host)).toBeNull())
  it('rejects RAM above the limit with the limit in the message', () => {
    const e = validateResources({ cpu: 4, ramGb: 25, storageGb: 40, gpu: false }, host)
    expect(e?.field).toBe('ramGb')
    expect(e?.message).toMatch(/24 GB/)
  })
  it('rejects CPU above the limit', () => expect(validateResources({ cpu: 13, ramGb: 8, storageGb: 40, gpu: false }, host)?.field).toBe('cpu'))
  it('rejects storage below the minimum', () => expect(validateResources({ cpu: 1, ramGb: 1, storageGb: 1, gpu: false }, host)?.field).toBe('storageGb'))
  it('rejects NaN input', () => expect(validateResources({ cpu: NaN, ramGb: 8, storageGb: 40, gpu: false }, host)?.field).toBe('cpu'))
  it('reports free-RAM shortfall', () => expect(validateResources({ cpu: 2, ramGb: 16, storageGb: 80, gpu: false }, { ...host, freeRamGb: 11.2 })?.message).toMatch(/11.2 GB free/))
  it('rejects GPU when unavailable', () => expect(validateResources({ cpu: 2, ramGb: 4, storageGb: 20, gpu: true }, host)?.field).toBe('gpu'))
})
