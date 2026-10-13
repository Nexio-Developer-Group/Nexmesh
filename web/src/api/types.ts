export type WorkspaceStatus = 'allocating' | 'preparing' | 'starting' | 'ready' | 'stopped' | 'failed'

export interface Workspace {
  id: string
  name: string
  description: string
  os: string
  cpu: number
  ramGb: number
  storageGb: number
  gpu: boolean
  status: WorkspaceStatus
  step: string
  error?: string
  createdAt: string
  lastOpenedAt: string
  runtimes: Record<string, { installed: string[]; active: string }>
  restartRequired: boolean
  agentConnected: boolean
}

export interface Host {
  maxCpu: number
  maxRamGb: number
  maxStorageGb: number
  freeRamGb: number
  gpuAvailable: boolean
  gpuReason?: string
}

export interface CatalogField {
  name: string
  label: string
  type: 'string' | 'number' | 'password' | 'boolean' | 'select' | 'textarea'
  required: boolean
  advanced: boolean
  default?: unknown
  placeholder?: string
  options?: string[]
  help?: string
}

export interface CatalogComponent {
  id: string
  name: string
  monogram: string
  categories: string[]
  kind: 'runtime' | 'framework' | 'service' | 'tool' | 'scripting'
  description: string
  supportedModes: ('install' | 'connect')[]
  versions: { default: string; supported: string[] }
  ports?: number[]
  install?: { strategy: string; image?: string; ports?: number[]; volumes?: { name: string; path: string; sizeGB: number }[]; requires?: string[] }
  resources?: { cpu: number; ramMB: number; storageGB: number }
  connection?: { schemes: string[]; defaultPort?: number; envPrefix: string; fields: CatalogField[] }
}

export interface Catalog {
  categories: { id: string; label: string; items: string[] }[]
  components: CatalogComponent[]
}

export type ComponentState =
  | 'draft' | 'installing' | 'running' | 'stopped' | 'failed'
  | 'testing' | 'test_failed' | 'test_passed' | 'connected' | 'degraded' | 'disconnected'

export interface StageResult { stage: string; status: 'pending' | 'running' | 'passed' | 'failed' | 'skipped'; message?: string }
export interface TestResult {
  passed: boolean
  failedStage?: string
  message?: string
  stages: StageResult[]
  latencyMs?: number
  serverVersion?: string
  at: string
}

export interface CanvasComponent {
  id: string
  workspaceId: string
  catalogId: string
  mode: '' | 'installed' | 'connected'
  version: string
  settings: Record<string, unknown>
  connection?: Record<string, unknown>
  connName?: string
  environment?: 'development' | 'staging' | 'production' | ''
  lastTest?: TestResult
  desiredState: string
  observedState: ComponentState
  message?: string
  progress: number
  x: number
  y: number
}

export interface Operation {
  id: string
  workspaceId: string
  componentId?: string
  type: string
  status: 'pending' | 'running' | 'succeeded' | 'failed' | 'cancelled'
  progress: number
  log: string[]
  error?: string
}
