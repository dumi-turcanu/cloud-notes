import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { publishNote, unpublishNote } from '../api/notes'
import type { Note } from '../api/types'
import { describeError } from '../lib/errors'
import { notesKey } from '../lib/queryClient'
import { secondaryButton } from './ui'

export function ShareControls({ note }: { note: Note }) {
  const queryClient = useQueryClient()
  const [shareUrl, setShareUrl] = useState<string>()
  const [copied, setCopied] = useState(false)
  const [error, setError] = useState<string>()

  const refresh = () => queryClient.invalidateQueries({ queryKey: notesKey })

  const publish = useMutation({
    mutationFn: () => publishNote(note.id),
    onSuccess: (published) => {
      setShareUrl(published.share_url)
      setError(undefined)
      return refresh()
    },
    onError: (err) => setError(describeError(err, 'The note could not be shared.')),
  })
  const unpublish = useMutation({
    mutationFn: () => unpublishNote(note.id),
    onSuccess: () => {
      setShareUrl(undefined)
      setCopied(false)
      setError(undefined)
      return refresh()
    },
    onError: (err) => setError(describeError(err, 'Sharing could not be stopped.')),
  })

  const url = shareUrl ?? `${window.location.origin}/shared/${note.share_uuid}`

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(url)
      setCopied(true)
      setError(undefined)
    } catch {
      setError('Copy failed. Select the link and copy it manually.')
    }
  }

  return (
    <div className="mt-4 border-t border-gray-700 pt-4">
      {note.is_public ? (
        <div>
          <label className="mb-1 block text-xs text-gray-400" htmlFor={`share-${note.id}`}>
            Public link
          </label>
          <div className="flex flex-wrap gap-2">
            <input
              id={`share-${note.id}`}
              readOnly
              value={url}
              onFocus={(e) => e.currentTarget.select()}
              className="min-w-0 flex-1 rounded-lg border border-gray-600 bg-gray-900 px-3 py-2 text-sm text-gray-200"
            />
            <button type="button" onClick={() => void copy()} className={secondaryButton}>
              {copied ? 'Copied' : 'Copy link'}
            </button>
            <button
              type="button"
              onClick={() => unpublish.mutate()}
              disabled={unpublish.isPending}
              className={secondaryButton}
            >
              Stop sharing
            </button>
          </div>
        </div>
      ) : (
        <button type="button" onClick={() => publish.mutate()} disabled={publish.isPending} className={secondaryButton}>
          {publish.isPending ? 'Sharing…' : 'Share publicly'}
        </button>
      )}
      {error && (
        <p role="alert" className="mt-2 text-sm text-red-300">
          {error}
        </p>
      )}
    </div>
  )
}
