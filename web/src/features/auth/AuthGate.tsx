import type { ReactNode } from 'react'
import { ApiError } from '@/api/client'
import { useMe } from '@/api/hooks'
import { SignInPage } from './SignInPage'

/** Renders the app only for a signed-in user; keeps the URL so sign-in returns to the same page. */
export function AuthGate({ children }: { children: ReactNode }) {
  const me = useMe()
  if (me.isPending) return <div role="status" className="grid min-h-screen place-items-center text-sm text-muted">Loading…</div>
  if (me.error instanceof ApiError && me.error.status === 401) return <SignInPage />
  if (me.error) return <div role="alert" className="grid min-h-screen place-items-center p-6 text-center text-sm text-err-fg">{me.error.message}</div>
  return <>{children}</>
}
