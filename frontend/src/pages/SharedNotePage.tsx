import { useQuery } from '@tanstack/react-query'
import { useParams } from 'react-router'
import { isApiError } from '../api/client'
import { getSharedNote } from '../api/notes'
import { ErrorBanner } from '../components/ErrorBanner'
import { cardClass } from '../components/ui'
import { useAppConfig } from '../context/useAppConfig'
import { describeError } from '../lib/errors'
import { NotFoundPage } from './NotFoundPage'

function SharedNote({ shareUuid }: { shareUuid: string }) {
  const note = useQuery({
    queryKey: ['shared', shareUuid],
    queryFn: ({ signal }) => getSharedNote(shareUuid, signal),
  })

  if (note.isPending) {
    return <p role="status" className="p-8 text-center text-blue-300">Loading note…</p>
  }
  if (note.isError) {
    if (isApiError(note.error) && note.error.status === 404) {
      return (
        <main className="mx-auto mt-24 max-w-md p-4 text-center">
          <h1 className="mb-2 text-3xl font-bold text-white">Note not found</h1>
          <p className="text-gray-400">This note does not exist or is no longer shared.</p>
        </main>
      )
    }
    return (
      <main className="mx-auto mt-16 max-w-2xl p-4">
        <ErrorBanner message={describeError(note.error, 'The note could not be loaded.')} />
      </main>
    )
  }

  const body = typeof note.data.content.body === 'string' ? note.data.content.body : ''
  return (
    <main className="mx-auto w-full max-w-2xl px-4 py-12">
      <article className={cardClass}>
        <h1 className="mb-4 break-words text-3xl font-bold text-white">{note.data.title}</h1>
        <p className="whitespace-pre-wrap break-words text-gray-200">{body}</p>
        <p className="mt-6 text-xs text-gray-400">
          Last updated {new Date(note.data.updated_at).toLocaleString()} · Shared with Cloud Notes
        </p>
      </article>
    </main>
  )
}

export function SharedNotePage() {
  const { features } = useAppConfig()
  const { shareUuid } = useParams()
  if (!features.public_sharing || !shareUuid) return <NotFoundPage />
  return <SharedNote shareUuid={shareUuid} />
}
