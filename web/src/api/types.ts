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
  /** Minutes of inactivity before the workspace stops itself; 0 = never. */
  idleStopMinutes: number
  stopReason?: 'user' | 'idle'
  /** Optional component summary, e.g. "6 components · 2 connected". */
  summary?: string
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
  order?: number
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
  warnings?: string[]
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
  /** Hint from a stack template; nothing happens until the user confirms. */
  suggestedMode?: 'install' | 'connect'
  environment?: 'development' | 'staging' | 'production' | ''
  lastTest?: TestResult
  desiredState: string
  observedState: ComponentState
  message?: string
  progress: number
  x: number
  y: number
  updatedAt?: string
}

export interface Operation {
  id: string
  workspaceId: string
  componentId?: string
  type: string
  params?: Record<string, unknown>
  status: 'pending' | 'running' | 'succeeded' | 'failed' | 'cancelled'
  progress: number
  log: string[]
  error?: string
}

// ---- code agent configuration (PRD 12)

export interface ProviderField {
  name: string; label: string; type: 'string' | 'password' | 'select' | 'boolean' | 'number'
  required?: boolean; advanced?: boolean; placeholder?: string; help?: string; options?: string[]
}

export interface AgentProvider {
  id: string; name: string; tagline: string; order: number; hosted: boolean; protocols: string[]
  baseUrl?: string; defaultModel?: string; notice?: string; comingSoon?: string[]; fields: ProviderField[]
}

export interface AgentConfig {
  id: string; name: string; provider: string; modelId: string; endpoint?: string; protocol?: string
  params: Record<string, unknown>; hasKey: boolean; lastTest?: TestResult; isDefault: boolean; createdAt: string
}

export interface AgentModel { id: string; name?: string; contextWindow?: number }
export interface AgentTestResult extends TestResult { models?: AgentModel[]; fingerprint?: string }

/** Body for test / create / update of an agent configuration. */
export interface AgentInput {
  name: string; provider: string; modelId: string; endpoint?: string; protocol?: string; apiKey?: string
  organization?: string; project?: string; authStyle?: string; headerName?: string; customHeaders?: Record<string, string>
  tlsInsecure?: boolean; caCertificate?: string
  maxOutputTokens?: number; temperature?: number; contextWindow?: number; timeoutSeconds?: number; retries?: number
  capabilities?: { streaming: boolean; tools: boolean; vision: boolean }
}

export interface SettingsPreview {
  changes: { field: string; from: unknown; to: unknown; appliesImmediately: boolean }[]
  restartRequired: boolean
}
