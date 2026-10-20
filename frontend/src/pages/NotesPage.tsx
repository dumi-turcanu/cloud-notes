import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createNote, listNotes } from '../api/notes'
import { ErrorBanner } from '../components/ErrorBanner'
import { NoteCard } from '../components/NoteCard'
import { NoteForm } from '../components/NoteForm'
import { cardClass } from '../components/ui'
import { describeError } from '../lib/errors'
import { notesKey } from '../lib/queryClient'

export function NotesPage() {
  const queryClient = useQueryClient()
  const notes = useQuery({ queryKey: notesKey, queryFn: ({ signal }) => listNotes(undefined, signal) })
  const create = useMutation({
    mutationFn: ({ title, body }: { title: string; body: string }) =>
      createNote({ title, content: { body } }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: notesKey }),
  })

  return (
    <>
      <section className={`${cardClass} mb-8`}>
        <NoteForm
          heading="Create a note"
          submitLabel="Save note"
          busyLabel="Saving…"
          resetOnSuccess
          onSubmit={async (title, body) => {
            await create.mutateAsync({ title, body })
          }}
        />
      </section>

      <h2 className="mb-4 text-2xl font-bold text-white">Your notes</h2>
      {notes.isError && <ErrorBanner message={describeError(notes.error, 'Notes could not be loaded.')} />}
      {notes.isPending && (
        <p role="status" className="animate-pulse text-blue-300">
          Loading notes…
        </p>
      )}
      {notes.data && notes.data.length === 0 && (
        <p className="italic text-gray-400">No notes yet. Create your first note above.</p>
      )}
      {notes.data && notes.data.length > 0 && (
        <ul className="grid grid-cols-1 gap-4 md:grid-cols-2">
          {notes.data.map((note) => (
            <li key={note.id}>
              <NoteCard note={note} />
            </li>
          ))}
        </ul>
      )}
    </>
  )
}
