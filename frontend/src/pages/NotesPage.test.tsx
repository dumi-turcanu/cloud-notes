import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import type { Note } from '../api/types'
import { baseHandlers, makeNote, mockApi } from '../test/mockApi'
import { renderApp } from '../test/renderApp'

describe('NotesPage', () => {
  it('lists notes as plain text', async () => {
    mockApi({
      ...baseHandlers({ authenticated: true }),
      'GET /api/notes': {
        body: [makeNote({ title: '<b>bold</b>', content: { body: '<script>alert(1)</script>\nline 2' } })],
      },
    })
    renderApp('/')
    expect(await screen.findByText('<b>bold</b>')).toBeInTheDocument()
    expect(screen.getByText(/<script>alert\(1\)<\/script>/)).toBeInTheDocument()
    expect(document.querySelector('script, b')).toBeNull()
  })

  it('shows an empty state', async () => {
    mockApi({ ...baseHandlers({ authenticated: true }), 'GET /api/notes': { body: [] } })
    renderApp('/')
    expect(await screen.findByText(/No notes yet/)).toBeInTheDocument()
  })

  it('shows an error banner when loading fails', async () => {
    mockApi({
      ...baseHandlers({ authenticated: true }),
      'GET /api/notes': { status: 500, body: { error: 'internal_error', message: 'Boom' } },
    })
    renderApp('/')
    expect(await screen.findByRole('alert')).toHaveTextContent('Boom')
  })

  it('creates a note (POST) and refetches the list', async () => {
    const user = userEvent.setup()
    const notes: Note[] = []
    const api = mockApi({
      ...baseHandlers({ authenticated: true }),
      'GET /api/notes': () => ({ body: notes }),
      'POST /api/notes': (req) => {
        const input = req.body as { title: string; content: { body: string } }
        notes.push(makeNote({ title: input.title, content: input.content }))
        return { status: 201, body: notes[0] }
      },
    })
    renderApp('/')
    await screen.findByText(/No notes yet/)
    const form = screen.getByRole('form', { name: 'Create a note' })
    await user.type(within(form).getByLabelText('Title'), 'Ideas')
    await user.type(within(form).getByLabelText('Note'), 'build things')
    await user.click(within(form).getByRole('button', { name: 'Save note' }))

    expect(await screen.findByRole('heading', { name: 'Ideas' })).toBeInTheDocument()
    expect(api.callsTo('POST /api/notes')[0].body).toEqual({ title: 'Ideas', content: { body: 'build things' } })
    expect(api.callsTo('GET /api/notes').length).toBeGreaterThanOrEqual(2)
    expect(within(form).getByLabelText('Title')).toHaveValue('')
  })

  it('requires a title', async () => {
    const user = userEvent.setup()
    const api = mockApi({ ...baseHandlers({ authenticated: true }), 'GET /api/notes': { body: [] } })
    renderApp('/')
    await user.click(await screen.findByRole('button', { name: 'Save note' }))
    expect(await screen.findByText('Title is required.')).toBeInTheDocument()
    expect(api.callsTo('POST /api/notes')).toHaveLength(0)
  })

  it('edits a note (PUT) preserving other content keys', async () => {
    const user = userEvent.setup()
    let note = makeNote({ content: { body: 'old', source: 'Web UI' } })
    const api = mockApi({
      ...baseHandlers({ authenticated: true }),
      'GET /api/notes': () => ({ body: [note] }),
      [`PUT /api/notes/${note.id}`]: (req) => {
        const input = req.body as Pick<Note, 'title' | 'content'>
        note = { ...note, ...input }
        return { body: note }
      },
    })
    renderApp('/')
    await user.click(await screen.findByRole('button', { name: 'Edit' }))
    const form = screen.getByRole('form', { name: 'Edit note' })
    const title = within(form).getByLabelText('Title')
    await user.clear(title)
    await user.type(title, 'Renamed')
    const text = within(form).getByLabelText('Note')
    await user.clear(text)
    await user.type(text, 'new text')
    await user.click(within(form).getByRole('button', { name: 'Save changes' }))

    expect(await screen.findByRole('heading', { name: 'Renamed' })).toBeInTheDocument()
    expect(screen.getByText('new text')).toBeInTheDocument()
    expect(api.callsTo(`PUT /api/notes/${note.id}`)[0].body).toEqual({
      title: 'Renamed',
      content: { body: 'new text', source: 'Web UI' },
    })
  })

  it('deletes only after confirmation', async () => {
    const user = userEvent.setup()
    let notes = [makeNote()]
    const id = notes[0].id
    const api = mockApi({
      ...baseHandlers({ authenticated: true }),
      'GET /api/notes': () => ({ body: notes }),
      [`DELETE /api/notes/${id}`]: () => {
        notes = []
        return { status: 204 }
      },
    })
    renderApp('/')
    await user.click(await screen.findByRole('button', { name: 'Delete' }))

    const dialog = screen.getByRole('alertdialog', { name: 'Move note to trash?' })
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(api.callsTo(`DELETE /api/notes/${id}`)).toHaveLength(0)

    await user.click(screen.getByRole('button', { name: 'Delete' }))
    await user.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Move to trash' }))
    expect(await screen.findByText(/No notes yet/)).toBeInTheDocument()
    expect(api.callsTo(`DELETE /api/notes/${id}`)).toHaveLength(1)
  })

  it('closes the confirmation dialog with Escape', async () => {
    const user = userEvent.setup()
    mockApi({ ...baseHandlers({ authenticated: true }), 'GET /api/notes': { body: [makeNote()] } })
    renderApp('/')
    await user.click(await screen.findByRole('button', { name: 'Delete' }))
    expect(screen.getByRole('button', { name: 'Cancel' })).toHaveFocus()
    await user.keyboard('{Escape}')
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  })

  it('hides share controls when public sharing is off', async () => {
    mockApi({ ...baseHandlers({ authenticated: true }), 'GET /api/notes': { body: [makeNote()] } })
    renderApp('/')
    await screen.findByText('Groceries')
    expect(screen.queryByRole('button', { name: 'Share publicly' })).not.toBeInTheDocument()
  })

  it('publishes and unpublishes when sharing is enabled', async () => {
    const user = userEvent.setup()
    let note = makeNote()
    const shareUrl = `https://notes.example.com/shared/${note.share_uuid}`
    const api = mockApi({
      ...baseHandlers({ authenticated: true, config: { features: { public_sharing: true } } }),
      'GET /api/notes': () => ({ body: [note] }),
      [`POST /api/notes/${note.id}/publish`]: () => {
        note = { ...note, is_public: true }
        return { body: { ...note, share_url: shareUrl } }
      },
      [`DELETE /api/notes/${note.id}/publish`]: () => {
        note = { ...note, is_public: false }
        return { status: 204 }
      },
    })
    renderApp('/')
    await user.click(await screen.findByRole('button', { name: 'Share publicly' }))
    expect(await screen.findByLabelText('Public link')).toHaveValue(shareUrl)
    expect(api.callsTo(`POST /api/notes/${note.id}/publish`)).toHaveLength(1)

    await user.click(screen.getByRole('button', { name: 'Stop sharing' }))
    expect(await screen.findByRole('button', { name: 'Share publicly' })).toBeInTheDocument()
  })
})
