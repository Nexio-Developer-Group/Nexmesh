import type { Host } from '@/api/types'

export interface ResourceValues { cpu: number; ramGb: number; storageGb: number; gpu: boolean }
export interface ResourceError { field: 'cpu' | 'ramGb' | 'storageGb' | 'gpu'; message: string }

export const MIN = { cpu: 1, ramGb: 1, storageGb: 5 }

/** Mirrors the server's runtime.ValidateResources so the form can explain limits immediately. */
export function validateResources(v: ResourceValues, host: Host): ResourceError | null {
  if (!Number.isFinite(v.cpu) || v.cpu < MIN.cpu || v.cpu > host.maxCpu)
    return { field: 'cpu', message: `CPU must be between ${MIN.cpu} and ${host.maxCpu} vCPU on this host.` }
  if (!Number.isFinite(v.ramGb) || v.ramGb < MIN.ramGb || v.ramGb > host.maxRamGb)
    return { field: 'ramGb', message: `RAM must be between ${MIN.ramGb} and ${host.maxRamGb} GB on this host.` }
  if (!Number.isFinite(v.storageGb) || v.storageGb < MIN.storageGb || v.storageGb > host.maxStorageGb)
    return { field: 'storageGb', message: `Storage must be between ${MIN.storageGb} and ${host.maxStorageGb} GB on this host.` }
  if (host.freeRamGb > 0 && v.ramGb > host.freeRamGb)
    return { field: 'ramGb', message: `Requested ${v.ramGb} GB RAM, host has ${host.freeRamGb.toFixed(1)} GB free.` }
  if (v.gpu && !host.gpuAvailable) return { field: 'gpu', message: 'GPU access is unavailable on this execution backend.' }
  return null
}
