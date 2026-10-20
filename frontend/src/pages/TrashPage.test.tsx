import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { baseHandlers, makeNote, mockApi } from '../test/mockApi'
import { renderApp } from '../test/renderApp'

describe('TrashPage', () => {
  it('shows an empty state', async () => {
    mockApi({ ...baseHandlers({ authenticated: true }), 'GET /api/notes/trash': { body: [] } })
    renderApp('/trash')
    expect(await screen.findByText('The trash is empty.')).toBeInTheDocument()
  })

  it('restores a note', async () => {
    const user = userEvent.setup()
    let trash = [makeNote({ title: 'Old idea', deleted_at: '2026-02-01T00:00:00Z' })]
    const id = trash[0].id
    const api = mockApi({
      ...baseHandlers({ authenticated: true }),
      'GET /api/notes/trash': () => ({ body: trash }),
      [`POST /api/notes/${id}/restore`]: () => {
        trash = []
        return { body: makeNote({ id }) }
      },
    })
    renderApp('/trash')
    expect(await screen.findByRole('heading', { name: 'Old idea' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Restore' }))
    expect(await screen.findByText('The trash is empty.')).toBeInTheDocument()
    expect(api.callsTo(`POST /api/notes/${id}/restore`)).toHaveLength(1)
  })
})
