import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { baseHandlers, mockApi } from '../test/mockApi'
import { renderApp } from '../test/renderApp'

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
