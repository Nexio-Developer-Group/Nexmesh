import type { ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { Logo } from '@/components/ui/Logo'

export function UserMenu() {
  return (
    <span aria-label="Account" className="grid size-8 place-items-center rounded-full bg-neutral-bg text-xs font-bold text-fg">
      AK
    </span>
  )
}

/** Shared page header: logo, breadcrumb, slot for page actions. */
export function TopBar({ crumbs, right }: { crumbs?: { label: string; to?: string }[]; right?: ReactNode }) {
  return (
    <header className="flex h-[60px] shrink-0 items-center gap-4 border-b border-line bg-panel px-6">
      <Link to="/" aria-label="Nexmesh home">
        <Logo />
      </Link>
      {crumbs && (
        <nav aria-label="Breadcrumb" className="flex items-center gap-2 border-l border-line pl-4 text-[13px]">
          {crumbs.map((c, i) => (
            <span key={c.label} className="flex items-center gap-2">
              {i > 0 && <span className="text-faint">/</span>}
              {c.to ? (
                <Link to={c.to} className="text-muted hover:text-fg">{c.label}</Link>
              ) : (
                <span className="font-semibold">{c.label}</span>
              )}
            </span>
          ))}
        </nav>
      )}
      <div className="ml-auto flex items-center gap-3">{right}</div>
      <UserMenu />
    </header>
  )
}
