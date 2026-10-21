import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { baseHandlers, makeNote, mockApi, oidcAuth, testUser, unauthorized } from '../test/mockApi'
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

  describe('single sign-on logout', () => {
    async function signOut(reply: { status: number; body?: unknown }) {
      const user = userEvent.setup()
      const assign = vi.spyOn(window.location, 'assign').mockImplementation(() => undefined)
      mockApi({
        ...baseHandlers({ authenticated: true, config: { auth: oidcAuth() } }),
        'GET /api/notes': { body: [] },
        'POST /api/auth/logout': reply,
      })
      renderApp('/')
      await user.click(await screen.findByRole('button', { name: 'Sign out' }))
      await screen.findByRole('link', { name: /Sign in with/ })
      return assign
    }

    it('sends the browser to the provider logout URL after clearing the local state', async () => {
      const assign = await signOut({ status: 200, body: { logout_url: 'https://auth.example.com/logout?client_id=x' } })
      expect(assign).toHaveBeenCalledWith('https://auth.example.com/logout?client_id=x')
    })

    it('stays in the app when there is no provider logout URL', async () => {
      const assign = await signOut({ status: 204 })
      expect(assign).not.toHaveBeenCalled()
    })

    it.each(['javascript:alert(1)', 'data:text/html,x', '/relative', 'not a url'])('never navigates to %s', async (url) => {
      const assign = await signOut({ status: 200, body: { logout_url: url } })
      expect(assign).not.toHaveBeenCalled()
    })
  })
})
