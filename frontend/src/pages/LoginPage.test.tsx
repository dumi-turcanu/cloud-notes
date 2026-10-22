import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AuthFlowError } from '../auth/errors'
import { sendReset, signInGoogle, signInPassword } from '../auth/identityPlatform'
import {
  baseHandlers,
  identityPlatformAuth,
  identityPlatformConfig,
  mockApi,
  oidcAuth,
  testUser,
  type MockHandler,
} from '../test/mockApi'
import { renderApp } from '../test/renderApp'

vi.mock('../auth/identityPlatform')

beforeEach(() => {
  vi.mocked(signInPassword).mockReset()
  vi.mocked(signInGoogle).mockReset()
  vi.mocked(sendReset).mockReset()
})

async function fillAndSubmit(email: string, password: string) {
  const user = userEvent.setup()
  const emailInput = await screen.findByLabelText('Email')
  if (email) await user.type(emailInput, email)
  if (password) await user.type(screen.getByLabelText('Password'), password)
  await user.click(screen.getByRole('button', { name: 'Sign in' }))
}

describe('LoginPage', () => {
  it('validates required fields on the client', async () => {
    const api = mockApi(baseHandlers())
    renderApp('/login')
    await fillAndSubmit('', '')
    expect(await screen.findByText('Email is required.')).toBeInTheDocument()
    expect(screen.getByText('Password is required.')).toBeInTheDocument()
    expect(api.callsTo('POST /api/auth/login')).toHaveLength(0)
  })

  it('rejects a malformed email without calling the API', async () => {
    const api = mockApi(baseHandlers())
    renderApp('/login')
    await fillAndSubmit('not-an-email', 'password-123456')
    expect(await screen.findByText('Enter a valid email address.')).toBeInTheDocument()
    expect(api.callsTo('POST /api/auth/login')).toHaveLength(0)
  })

  it('shows a generic message for invalid credentials', async () => {
    mockApi({
      ...baseHandlers(),
      'POST /api/auth/login': {
        status: 401,
        body: { error: 'invalid_credentials', message: 'Invalid email or password.' },
      },
    })
    renderApp('/login')
    await fillAndSubmit('ann@example.com', 'wrong-password-1')
    expect(await screen.findByRole('alert')).toHaveTextContent('Invalid email or password.')
  })

  it('shows the lockout duration from Retry-After', async () => {
    mockApi({
      ...baseHandlers(),
      'POST /api/auth/login': {
        status: 429,
        body: { error: 'too_many_attempts', message: 'locked' },
        headers: { 'Retry-After': '600' },
      },
    })
    renderApp('/login')
    await fillAndSubmit('ann@example.com', 'wrong-password-1')
    expect(await screen.findByRole('alert')).toHaveTextContent('Try again in 10 minutes.')
  })

  it('links to registration only when sign-up is enabled', async () => {
    mockApi(baseHandlers({ config: { signup_mode: 'disabled' } }))
    renderApp('/login')
    await screen.findByRole('heading', { name: 'Sign in' })
    expect(screen.queryByRole('link', { name: 'Create one' })).not.toBeInTheDocument()
  })
})

describe('LoginPage with single sign-on (oidc)', () => {
  const ssoHandlers = (extra: Record<string, MockHandler> = {}) => ({
    ...baseHandlers({ config: { auth: oidcAuth('Acme SSO') } }),
    ...extra,
  })

  it('shows one button that starts the redirect flow, and no password form', async () => {
    const api = mockApi(ssoHandlers())
    renderApp('/login')
    const link = await screen.findByRole('link', { name: 'Sign in with Acme SSO' })
    expect(link).toHaveAttribute('href', '/api/auth/oidc/login?from=%2F')
    expect(screen.queryByLabelText('Email')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Password')).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Create one' })).not.toBeInTheDocument()
    expect(link.closest('form')).toBeNull() // form-action 'self' would not allow the redirect chain
    expect(api.callsTo('POST /api/auth/login')).toHaveLength(0)
  })

  it('carries the page the user wanted through the login', async () => {
    mockApi(ssoHandlers())
    renderApp('/login?from=%2Ftrash%3Fx%3D1')
    expect(await screen.findByRole('link', { name: /Sign in with/ })).toHaveAttribute(
      'href',
      '/api/auth/oidc/login?from=%2Ftrash%3Fx%3D1',
    )
  })

  it('never forwards an unsafe from', async () => {
    mockApi(ssoHandlers())
    renderApp('/login?from=%2F%2Fevil.example')
    expect(await screen.findByRole('link', { name: /Sign in with/ })).toHaveAttribute('href', '/api/auth/oidc/login?from=%2F')
  })

  it('falls back to a generic label', async () => {
    mockApi(baseHandlers({ config: { auth: { provider: 'oidc' } } }))
    renderApp('/login')
    expect(await screen.findByRole('link', { name: 'Sign in with Single sign-on' })).toBeInTheDocument()
  })

  it.each([
    ['oidc_cancelled', 'Sign-in was cancelled.'],
    ['oidc_failed', 'Sign-in failed. Please try again.'],
    ['account_conflict', 'This email address is already used by another account.'],
    ['email_claim_missing', 'Your identity provider did not return a verified email address.'],
  ])('explains ?error=%s', async (code, message) => {
    mockApi(ssoHandlers())
    renderApp(`/login?error=${code}`)
    expect(await screen.findByRole('alert')).toHaveTextContent(message)
  })

  it('does not echo unknown error codes', async () => {
    mockApi(ssoHandlers())
    renderApp('/login?error=%3Cb%3Eboom%3C%2Fb%3E')
    await screen.findByRole('link', { name: /Sign in with/ })
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.queryByText(/boom/)).not.toBeInTheDocument()
  })

  it('goes straight to the app when a session already exists', async () => {
    mockApi({ ...baseHandlers({ config: { auth: oidcAuth() }, authenticated: true }), 'GET /api/notes': { body: [] } })
    renderApp('/login')
    expect(await screen.findByText(/Signed in as Ann/)).toBeInTheDocument()
  })
})

describe('LoginPage with Identity Platform', () => {
  const idpHandlers = (methods: Array<'password' | 'google'> = ['password'], extra: Record<string, MockHandler> = {}) => ({
    ...baseHandlers({ config: { auth: identityPlatformAuth({ sign_in_methods: methods }) } }),
    'GET /api/notes': { body: [] },
    ...extra,
  })

  it('signs in with the SDK and exchanges the ID token for a session', async () => {
    vi.mocked(signInPassword).mockResolvedValue('firebase-id-token')
    const api = mockApi(idpHandlers(['password'], { 'POST /api/auth/session': { body: testUser } }))
    renderApp('/login')
    await fillAndSubmit('ann@example.com', 'correct horse battery')

    expect(await screen.findByText(/Signed in as Ann/)).toBeInTheDocument()
    expect(signInPassword).toHaveBeenCalledWith(identityPlatformConfig, 'ann@example.com', 'correct horse battery')
    expect(api.callsTo('POST /api/auth/session')[0].body).toEqual({ id_token: 'firebase-id-token' })
    expect(api.callsTo('POST /api/auth/login')).toHaveLength(0)
  })

  it('shows the SDK error in plain words', async () => {
    vi.mocked(signInPassword).mockRejectedValue(new AuthFlowError('invalid_credentials', 'Invalid email or password.'))
    const api = mockApi(idpHandlers())
    renderApp('/login')
    await fillAndSubmit('ann@example.com', 'wrong-password-1')
    expect(await screen.findByRole('alert')).toHaveTextContent('Invalid email or password.')
    expect(api.callsTo('POST /api/auth/session')).toHaveLength(0)
  })

  it('reports an account conflict from the API', async () => {
    vi.mocked(signInPassword).mockResolvedValue('tok')
    mockApi(idpHandlers(['password'], {
      'POST /api/auth/session': { status: 409, body: { error: 'account_conflict', message: 'conflict' } },
    }))
    renderApp('/login')
    await fillAndSubmit('ann@example.com', 'correct horse battery')
    expect(await screen.findByRole('alert')).toHaveTextContent('This email address is already used by another account.')
  })

  it('validates required fields before calling the SDK', async () => {
    mockApi(idpHandlers())
    renderApp('/login')
    await fillAndSubmit('', '')
    expect(await screen.findByText('Email is required.')).toBeInTheDocument()
    expect(signInPassword).not.toHaveBeenCalled()
  })

  it('offers Google only when enabled', async () => {
    mockApi(idpHandlers(['password']))
    renderApp('/login')
    await screen.findByRole('button', { name: 'Sign in' })
    expect(screen.queryByRole('button', { name: 'Continue with Google' })).not.toBeInTheDocument()
  })

  it('signs in with Google through the pop-up', async () => {
    const user = userEvent.setup()
    vi.mocked(signInGoogle).mockResolvedValue('google-id-token')
    const api = mockApi(idpHandlers(['password', 'google'], { 'POST /api/auth/session': { body: testUser } }))
    renderApp('/login')
    await user.click(await screen.findByRole('button', { name: 'Continue with Google' }))
    expect(await screen.findByText(/Signed in as Ann/)).toBeInTheDocument()
    expect(api.callsTo('POST /api/auth/session')[0].body).toEqual({ id_token: 'google-id-token' })
  })

  it('stays quiet when the Google pop-up is closed', async () => {
    const user = userEvent.setup()
    vi.mocked(signInGoogle).mockRejectedValue(new AuthFlowError('cancelled', 'Sign-in was cancelled.'))
    mockApi(idpHandlers(['password', 'google']))
    renderApp('/login')
    await user.click(await screen.findByRole('button', { name: 'Continue with Google' }))
    await waitFor(() => expect(signInGoogle).toHaveBeenCalled())
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('shows only Google when the password method is off', async () => {
    mockApi(idpHandlers(['google']))
    renderApp('/login')
    expect(await screen.findByRole('button', { name: 'Continue with Google' })).toBeInTheDocument()
    expect(screen.queryByLabelText('Password')).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Create one' })).not.toBeInTheDocument()
  })

  it('sends a password reset e-mail and does not reveal whether the account exists', async () => {
    const user = userEvent.setup()
    vi.mocked(sendReset).mockResolvedValue(undefined)
    mockApi(idpHandlers())
    renderApp('/login')
    await user.type(await screen.findByLabelText('Email'), 'ann@example.com')
    await user.click(screen.getByRole('button', { name: 'Forgot password?' }))
    expect(await screen.findByRole('status')).toHaveTextContent('If an account exists for this address')
    expect(sendReset).toHaveBeenCalledWith(identityPlatformConfig, 'ann@example.com')
  })

  it('asks for the e-mail before sending a reset', async () => {
    const user = userEvent.setup()
    mockApi(idpHandlers())
    renderApp('/login')
    await user.click(await screen.findByRole('button', { name: 'Forgot password?' }))
    expect(await screen.findByText(/Enter your email address first/)).toBeInTheDocument()
    expect(sendReset).not.toHaveBeenCalled()
  })

  it('links to registration whatever the local sign-up mode says', async () => {
    mockApi({
      ...idpHandlers(),
      ...baseHandlers({ config: { signup_mode: 'disabled', auth: identityPlatformAuth() } }),
    })
    renderApp('/login')
    expect(await screen.findByRole('link', { name: 'Create one' })).toBeInTheDocument()
  })

  it('has no local password reset in local mode', async () => {
    mockApi(baseHandlers())
    renderApp('/login')
    await screen.findByRole('heading', { name: 'Sign in' })
    expect(screen.queryByRole('button', { name: 'Forgot password?' })).not.toBeInTheDocument()
  })
})
