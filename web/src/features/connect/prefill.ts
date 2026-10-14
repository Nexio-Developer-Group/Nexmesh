import { create } from 'zustand'

/**
 * Values imported from a .env file, connection string or compose file, held in
 * memory only until the Connect form for that card opens. Secrets never reach
 * storage: the form shows them for review and nothing is saved until Test
 * Connection passes and the user adds the connection (PRD 11.5, F14).
 */
export interface Prefill {
  connName: string
  fields: Record<string, unknown>
  secrets: Record<string, string>
}

interface State {
  byComponent: Record<string, Prefill>
  set: (componentId: string, p: Prefill) => void
  take: (componentId: string) => Prefill | undefined
}

export const usePrefill = create<State>((set, get) => ({
  byComponent: {},
  set: (id, p) => set((s) => ({ byComponent: { ...s.byComponent, [id]: p } })),
  take: (id) => {
    const p = get().byComponent[id]
    if (p) set((s) => { const { [id]: _drop, ...rest } = s.byComponent; return { byComponent: rest } })
    return p
  },
}))
