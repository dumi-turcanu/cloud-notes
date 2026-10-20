import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { deleteNote, updateNote } from '../api/notes'
import type { Note } from '../api/types'
import { useAppConfig } from '../context/useAppConfig'
import { describeError } from '../lib/errors'
import { notesKey } from '../lib/queryClient'
import { ConfirmDialog } from './ConfirmDialog'
import { NoteForm } from './NoteForm'
import { ShareControls } from './ShareControls'
import { cardClass, dangerButton, secondaryButton } from './ui'

export function NoteCard({ note }: { note: Note }) {
  const queryClient = useQueryClient()
  const { features } = useAppConfig()
  const [editing, setEditing] = useState(false)
  const [confirming, setConfirming] = useState(false)
  const [deleteError, setDeleteError] = useState<string>()

  const body = typeof note.content.body === 'string' ? note.content.body : ''

  const save = useMutation({
    // Keep unknown content keys; only the text body is edited by this UI.
    mutationFn: ({ title, body }: { title: string; body: string }) =>
      updateNote(note.id, { title, content: { ...note.content, body } }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: notesKey }),
  })
  const remove = useMutation({
    mutationFn: () => deleteNote(note.id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: notesKey }),
    onError: (err) => {
      setConfirming(false)
      setDeleteError(describeError(err, 'The note could not be deleted.'))
    },
  })

  if (editing) {
    return (
      <article className={cardClass}>
        <NoteForm
          heading="Edit note"
          initialTitle={note.title}
          initialBody={body}
          submitLabel="Save changes"
          busyLabel="Saving…"
          onSubmit={async (title, newBody) => {
            await save.mutateAsync({ title, body: newBody })
            setEditing(false)
          }}
          onCancel={() => setEditing(false)}
        />
      </article>
    )
  }

  return (
    <article className={`${cardClass} transition hover:border-blue-500`}>
      <h3 className="mb-2 break-words text-xl font-bold text-white">{note.title}</h3>
      {body && <p className="whitespace-pre-wrap break-words text-gray-200">{body}</p>}
      <p className="mt-4 text-xs text-gray-400">
        Updated {new Date(note.updated_at).toLocaleString()}
      </p>
      {deleteError && (
        <p role="alert" className="mt-2 text-sm text-red-300">
          {deleteError}
        </p>
      )}
      <div className="mt-4 flex gap-3">
        <button type="button" onClick={() => setEditing(true)} className={secondaryButton}>
          Edit
        </button>
        <button
          type="button"
          onClick={() => {
            setDeleteError(undefined)
            setConfirming(true)
          }}
          className={dangerButton}
        >
          Delete
        </button>
      </div>
      {features.public_sharing && <ShareControls note={note} />}
      {confirming && (
        <ConfirmDialog
          title="Move note to trash?"
          message={`“${note.title}” will be moved to the trash. You can restore it from there.`}
          confirmLabel="Move to trash"
          busy={remove.isPending}
          onConfirm={() => remove.mutate()}
          onCancel={() => setConfirming(false)}
        />
      )}
    </article>
  )
}
