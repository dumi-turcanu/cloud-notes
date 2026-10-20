import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { baseHandlers, mockApi } from '../test/mockApi'
import { renderApp } from '../test/renderApp'

const uuid = '33333333-3333-4333-8333-333333333333'

describe('SharedNotePage', () => {
  it('renders a shared note without requiring a session', async () => {
    mockApi({
      ...baseHandlers({ config: { features: { public_sharing: true } } }),
      [`GET /api/public/notes/${uuid}`]: {
        body: { title: 'Shared title', content: { body: 'hello\nworld' }, updated_at: '2026-01-02T00:00:00Z' },
      },
    })
    renderApp(`/shared/${uuid}`)
    expect(await screen.findByRole('heading', { name: 'Shared title' })).toBeInTheDocument()
    expect(screen.getByText(/hello/)).toBeInTheDocument()
  })

  it('shows a not-found state for unknown notes', async () => {
    mockApi({
      ...baseHandlers({ config: { features: { public_sharing: true } } }),
      [`GET /api/public/notes/${uuid}`]: { status: 404, body: { error: 'not_found', message: 'not found' } },
    })
    renderApp(`/shared/${uuid}`)
    expect(await screen.findByRole('heading', { name: 'Note not found' })).toBeInTheDocument()
  })

  it('is a 404 page when public sharing is disabled', async () => {
    const api = mockApi(baseHandlers())
    renderApp(`/shared/${uuid}`)
    expect(await screen.findByRole('heading', { name: 'Page not found' })).toBeInTheDocument()
    expect(api.callsTo(`GET /api/public/notes/${uuid}`)).toHaveLength(0)
  })
})
