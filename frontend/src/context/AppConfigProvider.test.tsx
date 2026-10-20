import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { mockApi } from '../test/mockApi'
import { renderApp } from '../test/renderApp'

describe('AppConfigProvider', () => {
  it('shows an error screen when the config cannot be loaded', async () => {
    mockApi({ 'GET /api/config': { status: 503, body: { error: 'internal_error', message: 'down' } } })
    renderApp('/')
    expect(await screen.findByRole('alert')).toHaveTextContent('Cloud Notes is unavailable')
    expect(screen.getByRole('button', { name: 'Try again' })).toBeInTheDocument()
  })

  it('renders unknown routes as 404', async () => {
    mockApi({
      'GET /api/config': { body: { signup_mode: 'disabled', features: { public_sharing: false }, version: 'x' } },
      'GET /api/auth/me': { status: 401, body: { error: 'unauthorized', message: 'no' } },
    })
    renderApp('/definitely/not/here')
    expect(await screen.findByRole('heading', { name: 'Page not found' })).toBeInTheDocument()
  })
})
