import { apiFetch } from './client'
import type { Note, NoteInput, PublishedNote, SharedNote } from './types'

export const listNotes = (since?: string, signal?: AbortSignal) =>
  apiFetch<Note[]>(since ? `/notes?since=${encodeURIComponent(since)}` : '/notes', { signal })

export const listTrash = (signal?: AbortSignal) => apiFetch<Note[]>('/notes/trash', { signal })

export const createNote = (input: NoteInput) =>
  apiFetch<Note>('/notes', { method: 'POST', body: input })

export const updateNote = (id: string, input: NoteInput) =>
  apiFetch<Note>(`/notes/${encodeURIComponent(id)}`, { method: 'PUT', body: input })

export const deleteNote = (id: string) =>
  apiFetch<void>(`/notes/${encodeURIComponent(id)}`, { method: 'DELETE' })

export const restoreNote = (id: string) =>
  apiFetch<Note>(`/notes/${encodeURIComponent(id)}/restore`, { method: 'POST' })

export const publishNote = (id: string) =>
  apiFetch<PublishedNote>(`/notes/${encodeURIComponent(id)}/publish`, { method: 'POST' })

export const unpublishNote = (id: string) =>
  apiFetch<void>(`/notes/${encodeURIComponent(id)}/publish`, { method: 'DELETE' })

export const getSharedNote = (shareUuid: string, signal?: AbortSignal) =>
  apiFetch<SharedNote>(`/public/notes/${encodeURIComponent(shareUuid)}`, { signal })
