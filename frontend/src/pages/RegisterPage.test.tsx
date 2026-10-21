import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AuthFlowError } from '../auth/errors'
import { signUpPassword } from '../auth/identityPlatform'
import { baseHandlers, identityPlatformAuth, identityPlatformConfig, mockApi, oidcAuth, testUser } from '../test/mockApi'
import { renderApp } from '../test/renderApp'

vi.mock('../auth/identityPlatform')

beforeEach(() => {
  vi.mocked(signUpPassword).mockReset()
})

describe('RegisterPage', () => {
  it('redirects to /login when sign-up is disabled', async () => {
    mockApi(baseHandlers({ config: { signup_mode: 'disabled' } }))
    const { router } = renderApp('/register')
    expect(await screen.findByRole('heading', { name: 'Sign in' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/login')
  })

  it('has no invite field in open mode', async () => {
    mockApi(baseHandlers())
    renderApp('/register')
    await screen.findByRole('heading', { name: 'Create your account' })
    expect(screen.queryByLabelText('Invite code')).not.toBeInTheDocument()
  })

  it('requires an invite code in invite mode and sends it', async () => {
    const user = userEvent.setup()
    const api = mockApi({
      ...baseHandlers({ config: { signup_mode: 'invite' } }),
      'POST /api/auth/register': { status: 201, body: testUser },
      'GET /api/notes': { body: [] },
    })
    renderApp('/register')
    await user.type(await screen.findByLabelText('Email'), 'ann@example.com')
    await user.type(screen.getByLabelText('Password'), 'a-long-enough-password')
    await user.click(screen.getByRole('button', { name: 'Create account' }))
    expect(await screen.findByText('An invite code is required.')).toBeInTheDocument()
    expect(api.callsTo('POST /api/auth/register')).toHaveLength(0)

    await user.type(screen.getByLabelText('Invite code'), 'let-me-in-please-123')
    await user.click(screen.getByRole('button', { name: 'Create account' }))
    expect(await screen.findByText(/Signed in as Ann/)).toBeInTheDocument()
    expect(api.callsTo('POST /api/auth/register')[0].body).toEqual({
      email: 'ann@example.com',
      password: 'a-long-enough-password',
      invite_code: 'let-me-in-please-123',
    })
  })

  it('enforces the 12 character password minimum client-side', async () => {
    const user = userEvent.setup()
    const api = mockApi(baseHandlers())
    renderApp('/register')
    await user.type(await screen.findByLabelText('Email'), 'ann@example.com')
    await user.type(screen.getByLabelText('Password'), 'short')
    await user.click(screen.getByRole('button', { name: 'Create account' }))
    expect(await screen.findByText('Password must be at least 12 characters.')).toBeInTheDocument()
    expect(api.callsTo('POST /api/auth/register')).toHaveLength(0)
  })

  it('shows field errors from validation_failed', async () => {
    const user = userEvent.setup()
    mockApi({
      ...baseHandlers(),
      'POST /api/auth/register': {
        status: 400,
        body: {
          error: 'validation_failed',
          message: 'Invalid input',
          fields: { email: 'must be a valid email address', password: 'is too common' },
        },
      },
    })
    renderApp('/register')
    await user.type(await screen.findByLabelText('Email'), 'ann@example.com')
    await user.type(screen.getByLabelText('Password'), 'a-long-enough-password')
    await user.click(screen.getByRole('button', { name: 'Create account' }))
    expect(await screen.findByText('must be a valid email address')).toBeInTheDocument()
    expect(screen.getByText('is too common')).toBeInTheDocument()
    expect(screen.getByLabelText('Email')).toHaveAttribute('aria-invalid', 'true')
  })

  it('reports an already registered email', async () => {
    const user = userEvent.setup()
    mockApi({
      ...baseHandlers(),
      'POST /api/auth/register': { status: 409, body: { error: 'email_taken', message: 'taken' } },
    })
    renderApp('/register')
    await user.type(await screen.findByLabelText('Email'), 'ann@example.com')
    await user.type(screen.getByLabelText('Password'), 'a-long-enough-password')
    await user.click(screen.getByRole('button', { name: 'Create account' }))
    expect(await screen.findByText('An account with this email already exists.')).toBeInTheDocument()
  })
})

describe('RegisterPage with single sign-on (oidc)', () => {
  it('redirects to /login: accounts are created on the provider page', async () => {
    mockApi(baseHandlers({ config: { auth: oidcAuth() } }))
    const { router } = renderApp('/register')
    expect(await screen.findByRole('link', { name: 'Sign in with Acme SSO' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/login')
  })

  it('redirects even when the local sign-up mode is open', async () => {
    mockApi(baseHandlers({ config: { signup_mode: 'open', auth: oidcAuth() } }))
    const { router } = renderApp('/register')
    await screen.findByRole('link', { name: /Sign in with/ })
    expect(router.state.location.pathname).toBe('/login')
  })
})

describe('RegisterPage with Identity Platform', () => {
  const idpHandlers = (config = {}) => ({
    ...baseHandlers({ config: { signup_mode: 'disabled', auth: identityPlatformAuth(), ...config } }),
    'POST /api/auth/session': { body: testUser },
    'GET /api/notes': { body: [] },
  })

  async function fill(user: ReturnType<typeof userEvent.setup>, password = 'a-long-enough-password') {
    await user.type(await screen.findByLabelText('Email'), 'ann@example.com')
    await user.type(screen.getByLabelText('Password'), password)
    await user.click(screen.getByRole('button', { name: 'Create account' }))
  }

  it('creates the account through the SDK, then exchanges the token for a session', async () => {
    const user = userEvent.setup()
    vi.mocked(signUpPassword).mockResolvedValue('new-user-id-token')
    const api = mockApi(idpHandlers())
    renderApp('/register')
    await fill(user)

    expect(await screen.findByText(/Signed in as Ann/)).toBeInTheDocument()
    expect(signUpPassword).toHaveBeenCalledWith(identityPlatformConfig, 'ann@example.com', 'a-long-enough-password')
    expect(api.callsTo('POST /api/auth/session')[0].body).toEqual({ id_token: 'new-user-id-token' })
    expect(api.callsTo('POST /api/auth/register')).toHaveLength(0)
  })

  it('ignores the local sign-up mode and has no invite or display name fields', async () => {
    mockApi(idpHandlers({ signup_mode: 'invite' }))
    renderApp('/register')
    await screen.findByRole('heading', { name: 'Create your account' })
    expect(screen.queryByLabelText('Invite code')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Display name (optional)')).not.toBeInTheDocument()
  })

  it('keeps the 12 character minimum', async () => {
    const user = userEvent.setup()
    mockApi(idpHandlers())
    renderApp('/register')
    await fill(user, 'short')
    expect(await screen.findByText('Password must be at least 12 characters.')).toBeInTheDocument()
    expect(signUpPassword).not.toHaveBeenCalled()
  })

  it.each([
    ['email_taken', 'An account with this email already exists.'],
    ['weak_password', 'That password is too weak.'],
  ])('shows %s next to the field', async (code, message) => {
    const user = userEvent.setup()
    vi.mocked(signUpPassword).mockRejectedValue(new AuthFlowError(code, message))
    mockApi(idpHandlers())
    renderApp('/register')
    await fill(user)
    expect(await screen.findByText(message)).toBeInTheDocument()
  })

  it('redirects to /login when the password method is off', async () => {
    mockApi(baseHandlers({ config: { auth: identityPlatformAuth({ sign_in_methods: ['google'] }) } }))
    const { router } = renderApp('/register')
    await screen.findByRole('heading', { name: 'Sign in' })
    expect(router.state.location.pathname).toBe('/login')
  })
})
