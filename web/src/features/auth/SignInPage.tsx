import { useState } from 'react'
import { ApiError } from '@/api/client'
import { useAuthStatus, useSignIn } from '@/api/hooks'
import { Button } from '@/components/ui/Button'
import { Field } from '@/components/ui/Field'
import { Logo } from '@/components/ui/Logo'

/** Sign-in / first-run account creation. Light theme per the design system's account screens. */
export function SignInPage() {
  const { data: status } = useAuthStatus()
  const [mode, setMode] = useState<'login' | 'register'>('login')
  const signIn = useSignIn(mode)
  const [email, setEmail] = useState('')
  const [name, setName] = useState('')
  const [password, setPassword] = useState('')
  const err = signIn.error instanceof ApiError ? signIn.error : null
  const first = status?.signupOpen && mode === 'register'

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    signIn.mutate({ email, password, ...(mode === 'register' ? { name } : {}) })
  }

  return (
    <main className="theme-light grid min-h-screen place-items-center bg-canvas px-4 text-fg">
      <div className="w-full max-w-[400px]">
        <div className="mb-6 flex justify-center"><Logo size={28} /></div>
        <form onSubmit={submit} className="space-y-4 rounded-card border border-line bg-panel p-6" aria-labelledby="signin-title">
          <h1 id="signin-title" className="text-lg font-bold">{mode === 'login' ? 'Sign in to Nexmesh' : 'Create your account'}</h1>
          {mode === 'register' && <Field label="Name" optional autoComplete="name" value={name} onChange={(e) => setName(e.target.value)} />}
          <Field label="Email" type="email" autoComplete="email" required value={email} onChange={(e) => setEmail(e.target.value)}
            error={err?.field === 'email' ? err.message : undefined} />
          <Field label="Password" type="password" required autoComplete={mode === 'login' ? 'current-password' : 'new-password'}
            value={password} onChange={(e) => setPassword(e.target.value)}
            hint={mode === 'register' ? 'At least 10 characters.' : undefined}
            error={err?.field === 'password' ? err.message : undefined} />
          {err && !err.field && <p role="alert" className="text-[13px] text-err-fg">{err.message}</p>}
          <Button type="submit" variant="primary" className="w-full" loading={signIn.isPending}>
            {mode === 'login' ? 'Sign in' : first ? 'Create owner account' : 'Create account'}
          </Button>
        </form>
        {(status?.signupOpen || mode === 'register') && (
          <p className="mt-4 text-center text-[13px] text-muted">
            {mode === 'login' ? 'New here? ' : 'Already have an account? '}
            <button type="button" className="font-semibold text-link hover:underline" onClick={() => { signIn.reset(); setMode(mode === 'login' ? 'register' : 'login') }}>
              {mode === 'login' ? 'Create an account' : 'Sign in'}
            </button>
          </p>
        )}
        {first && <p className="mt-2 text-center text-xs text-faint">The first account on this instance becomes its owner.</p>}
      </div>
    </main>
  )
}
