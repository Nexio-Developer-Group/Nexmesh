import { useState, type ReactNode } from 'react'
import { useMe, useSignOut } from '@/api/hooks'
import { Link } from 'react-router-dom'
import { Logo } from '@/components/ui/Logo'

export function UserMenu() {
  const { data: me } = useMe()
  const signOut = useSignOut()
  const [open, setOpen] = useState(false)
  const initials = (me?.name ?? '?').split(/\s+/).map((p) => p[0]).slice(0, 2).join('').toUpperCase()
  return (
    <div className="relative">
      <button
        type="button"
        aria-label="Account"
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen((o) => !o)}
        className="grid size-8 place-items-center rounded-full bg-neutral-bg text-xs font-bold text-fg"
      >
        {initials}
      </button>
      {open && (
        <div role="menu" className="absolute right-0 top-10 z-50 w-56 rounded-card border border-line bg-panel p-3 shadow-lg">
          <p className="truncate text-[13px] font-semibold">{me?.name}</p>
          <p className="mb-2 truncate text-xs text-muted">{me?.email}</p>
          <button role="menuitem" type="button" onClick={() => signOut.mutate()} className="w-full rounded-lg border border-line-strong px-3 py-1.5 text-left text-[13px] hover:border-blue">
            Sign out
          </button>
        </div>
      )}
    </div>
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
