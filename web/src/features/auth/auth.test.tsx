import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AuthGate } from './AuthGate'
import { mockApi, renderPage } from '@/test/utils'

const unauth = { status: 401, body: { error: { code: 'unauthenticated', message: 'Sign in to continue.' } } }

describe('AuthGate', () => {
  it('shows the app when signed in', async () => {
    mockApi({ 'GET /api/auth/me': () => ({ user: { id: 'u1', email: 'a@b.co', name: 'Ada', role: 'owner' } }) })
    renderPage(<AuthGate><p>secret app</p></AuthGate>)
    expect(await screen.findByText('secret app')).toBeInTheDocument()
  })

  it('shows sign-in on 401 and signs in', async () => {
    let signedIn = false
    const calls = mockApi({
      'GET /api/auth/me': () => (signedIn ? { user: { id: 'u1', email: 'a@b.co', name: 'Ada', role: 'owner' } } : unauth),
      'GET /api/auth/status': () => ({ enabled: true, signupOpen: false }),
      'POST /api/auth/login': () => { signedIn = true; return { user: { id: 'u1', email: 'a@b.co', name: 'Ada', role: 'owner' } } },
    })
    renderPage(<AuthGate><p>secret app</p></AuthGate>)
    await userEvent.type(await screen.findByLabelText('Email'), 'a@b.co')
    await userEvent.type(screen.getByLabelText('Password'), 'correct horse battery')
    await userEvent.click(screen.getByRole('button', { name: 'Sign in' }))
    expect(await screen.findByText('secret app')).toBeInTheDocument()
    expect(calls.find((c) => c.url === '/api/auth/login')?.body).toEqual({ email: 'a@b.co', password: 'correct horse battery' })
    expect(screen.queryByText(/create an account/i)).not.toBeInTheDocument()
  })

  it('offers owner creation on a fresh instance and shows field errors', async () => {
    mockApi({
      'GET /api/auth/me': () => unauth,
      'GET /api/auth/status': () => ({ enabled: true, signupOpen: true }),
      'POST /api/auth/register': () => ({ status: 422, body: { error: { code: 'invalid', message: 'Use at least 10 characters.', field: 'password' } } }),
    })
    renderPage(<AuthGate><p>secret app</p></AuthGate>)
    await userEvent.click(await screen.findByRole('button', { name: 'Create an account' }))
    await userEvent.type(screen.getByLabelText('Email'), 'a@b.co')
    await userEvent.type(screen.getByLabelText('Password'), 'short')
    await userEvent.click(screen.getByRole('button', { name: /create owner account/i }))
    await waitFor(() => expect(screen.getByText('Use at least 10 characters.')).toBeInTheDocument())
  })
})
