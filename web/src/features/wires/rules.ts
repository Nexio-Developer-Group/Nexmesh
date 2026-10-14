import type { CatalogComponent } from '@/api/types'

/**
 * The catalog decides which cards may be connected (PRD 18.4 rule 5). This
 * mirrors the server's check so the canvas can refuse an impossible line while
 * it is being dragged; the server remains the authority.
 */
export function canConnect(from: CatalogComponent | undefined, to: CatalogComponent | undefined): { ok: boolean; reason?: string } {
  if (!from || !to) return { ok: false, reason: 'Unknown card.' }
  if (!from.wiring?.consumes?.length) return { ok: false, reason: `${from.name} does not take connections from other cards.` }
  if (!to.wiring?.provides) return { ok: false, reason: `Other cards cannot connect to ${to.name}.` }
  if (from.wiring.consumes.some((c) => to.categories.includes(c))) return { ok: true }
  return { ok: false, reason: `${from.name} connects to ${from.wiring.consumes.join(' or ')} cards, and ${to.name} is ${to.categories.join('/')}.` }
}
