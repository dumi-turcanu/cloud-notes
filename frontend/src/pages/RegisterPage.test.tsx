import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { baseHandlers, mockApi, testUser } from '../test/mockApi'
import { renderApp } from '../test/renderApp'

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
