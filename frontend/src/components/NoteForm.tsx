import { useState, type FormEvent } from 'react'
import { isApiError } from '../api/client'
import { describeError, runeLength } from '../lib/errors'
import { ErrorBanner } from './ErrorBanner'
import { TextField } from './TextField'
import { primaryButton, secondaryButton } from './ui'

interface NoteFormProps {
  heading?: string
  initialTitle?: string
  initialBody?: string
  submitLabel: string
  busyLabel: string
  onSubmit: (title: string, body: string) => Promise<void>
  onCancel?: () => void
  /** Clear the inputs after a successful submit (create form). */
  resetOnSuccess?: boolean
}

export function NoteForm({
  heading,
  initialTitle = '',
  initialBody = '',
  submitLabel,
  busyLabel,
  onSubmit,
  onCancel,
  resetOnSuccess,
}: NoteFormProps) {
  const [title, setTitle] = useState(initialTitle)
  const [body, setBody] = useState(initialBody)
  const [busy, setBusy] = useState(false)
  const [titleError, setTitleError] = useState<string>()
  const [formError, setFormError] = useState<string>()

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    setFormError(undefined)
    const trimmed = title.trim()
    if (!trimmed) {
      setTitleError('Title is required.')
      return
    }
    if (runeLength(trimmed) > 255) {
      setTitleError('Title must be at most 255 characters.')
      return
    }
    setTitleError(undefined)
    setBusy(true)
    try {
      await onSubmit(trimmed, body)
      if (resetOnSuccess) {
        setTitle('')
        setBody('')
      }
    } catch (err) {
      if (isApiError(err) && err.fields.title) setTitleError(err.fields.title)
      else setFormError(describeError(err, 'The note could not be saved.'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={(e) => void handleSubmit(e)} noValidate aria-label={heading ?? submitLabel}>
      {heading && <h2 className="mb-4 text-xl font-bold text-white">{heading}</h2>}
      <ErrorBanner message={formError} />
      <TextField label="Title" value={title} onChange={setTitle} error={titleError} disabled={busy} maxLength={255} />
      <TextField label="Note" value={body} onChange={setBody} disabled={busy} multiline />
      <div className="flex gap-3">
        <button type="submit" disabled={busy} className={primaryButton}>
          {busy ? busyLabel : submitLabel}
        </button>
        {onCancel && (
          <button type="button" onClick={onCancel} disabled={busy} className={secondaryButton}>
            Cancel
          </button>
        )}
      </div>
    </form>
  )
}
