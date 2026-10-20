import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { baseHandlers, mockApi } from '../test/mockApi'
import { renderApp } from '../test/renderApp'

describe('RequireAuth', () => {
  it('redirects anonymous users to /login with the return path', async () => {
    mockApi(baseHandlers())
    const { router } = renderApp('/trash')
    expect(await screen.findByRole('heading', { name: 'Sign in' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/login')
    expect(router.state.location.search).toBe(`?from=${encodeURIComponent('/trash')}`)
  })

  it('returns to the original page after login', async () => {
    const user = userEvent.setup()
    mockApi({
      ...baseHandlers(),
      'POST /api/auth/login': {
        body: { id: 'u', email: 'a@b.co', display_name: null, created_at: '2026-01-01T00:00:00Z' },
      },
      'GET /api/notes/trash': { body: [] },
    })
    const { router } = renderApp('/trash')
    await user.type(await screen.findByLabelText('Email'), 'a@b.co')
    await user.type(screen.getByLabelText('Password'), 'whatever-password')
    await user.click(screen.getByRole('button', { name: 'Sign in' }))
    expect(await screen.findByText('The trash is empty.')).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/trash')
  })

  it('ignores unsafe return targets', async () => {
    const user = userEvent.setup()
    mockApi({
      ...baseHandlers(),
      'POST /api/auth/login': {
        body: { id: 'u', email: 'a@b.co', display_name: null, created_at: '2026-01-01T00:00:00Z' },
      },
      'GET /api/notes': { body: [] },
    })
    const { router } = renderApp(`/login?from=${encodeURIComponent('//evil.example')}`)
    await user.type(await screen.findByLabelText('Email'), 'a@b.co')
    await user.type(screen.getByLabelText('Password'), 'whatever-password')
    await user.click(screen.getByRole('button', { name: 'Sign in' }))
    expect(await screen.findByText(/No notes yet/)).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/')
  })
})
