import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { baseHandlers, makeNote, mockApi, testUser, unauthorized } from '../test/mockApi'
import { renderApp } from '../test/renderApp'

describe('AuthProvider', () => {
  it('becomes anonymous when /me returns 401', async () => {
    mockApi(baseHandlers())
    const { router } = renderApp('/')
    expect(await screen.findByRole('heading', { name: 'Sign in' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/login')
  })

  it('logs in, then logs out', async () => {
    const user = userEvent.setup()
    const api = mockApi({
      ...baseHandlers(),
      'POST /api/auth/login': { body: testUser },
      'POST /api/auth/logout': { status: 204 },
      'GET /api/notes': { body: [] },
    })
    renderApp('/login')
    await user.type(await screen.findByLabelText('Email'), 'ann@example.com')
    await user.type(screen.getByLabelText('Password'), 'correct horse battery')
    await user.click(screen.getByRole('button', { name: 'Sign in' }))

    expect(await screen.findByText(/Signed in as Ann/)).toBeInTheDocument()
    expect(api.callsTo('POST /api/auth/login')[0].body).toEqual({
      email: 'ann@example.com',
      password: 'correct horse battery',
    })

    await user.click(screen.getByRole('button', { name: 'Sign out' }))
    expect(await screen.findByRole('heading', { name: 'Sign in' })).toBeInTheDocument()
    expect(api.callsTo('POST /api/auth/logout')).toHaveLength(1)
  })

  it('goes anonymous when a notes query answers 401', async () => {
    mockApi({ ...baseHandlers({ authenticated: true }), 'GET /api/notes': unauthorized })
    renderApp('/')
    expect(await screen.findByRole('heading', { name: 'Sign in' })).toBeInTheDocument()
  })

  it('drops cached notes when the session ends', async () => {
    const user = userEvent.setup()
    mockApi({
      ...baseHandlers({ authenticated: true }),
      'GET /api/notes': { body: [makeNote()] },
      'POST /api/auth/logout': { status: 204 },
    })
    const { client } = renderApp('/')
    expect(await screen.findByText('Groceries')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Sign out' }))
    await waitFor(() => expect(client.getQueryData(['notes'])).toBeUndefined())
  })
})
