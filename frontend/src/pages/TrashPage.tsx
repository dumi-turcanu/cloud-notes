import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { listTrash, restoreNote } from '../api/notes'
import type { Note } from '../api/types'
import { ErrorBanner } from '../components/ErrorBanner'
import { cardClass, secondaryButton } from '../components/ui'
import { describeError } from '../lib/errors'
import { notesKey, trashKey } from '../lib/queryClient'

function TrashCard({ note }: { note: Note }) {
  const queryClient = useQueryClient()
  const [error, setError] = useState<string>()
  const restore = useMutation({
    mutationFn: () => restoreNote(note.id),
    // Invalidating ['notes'] covers both the active list and the trash.
    onSuccess: () => queryClient.invalidateQueries({ queryKey: notesKey }),
    onError: (err) => setError(describeError(err, 'The note could not be restored.')),
  })
  const body = typeof note.content.body === 'string' ? note.content.body : ''

  return (
    <article className={cardClass}>
      <h3 className="mb-2 break-words text-xl font-bold text-white">{note.title}</h3>
      {body && <p className="whitespace-pre-wrap break-words text-gray-300">{body}</p>}
      <p className="mt-4 text-xs text-gray-400">
        Deleted {note.deleted_at ? new Date(note.deleted_at).toLocaleString() : ''}
      </p>
      {error && (
        <p role="alert" className="mt-2 text-sm text-red-300">
          {error}
        </p>
      )}
      <button
        type="button"
        onClick={() => {
          setError(undefined)
          restore.mutate()
        }}
        disabled={restore.isPending}
        className={`${secondaryButton} mt-4`}
      >
        {restore.isPending ? 'Restoring…' : 'Restore'}
      </button>
    </article>
  )
}

export function TrashPage() {
  const trash = useQuery({ queryKey: trashKey, queryFn: ({ signal }) => listTrash(signal) })

  return (
    <>
      <h2 className="mb-4 text-2xl font-bold text-white">Trash</h2>
      {trash.isError && <ErrorBanner message={describeError(trash.error, 'The trash could not be loaded.')} />}
      {trash.isPending && (
        <p role="status" className="animate-pulse text-blue-300">
          Loading trash…
        </p>
      )}
      {trash.data && trash.data.length === 0 && <p className="italic text-gray-400">The trash is empty.</p>}
      {trash.data && trash.data.length > 0 && (
        <ul className="grid grid-cols-1 gap-4 md:grid-cols-2">
          {trash.data.map((note) => (
            <li key={note.id}>
              <TrashCard note={note} />
            </li>
          ))}
        </ul>
      )}
    </>
  )
}
