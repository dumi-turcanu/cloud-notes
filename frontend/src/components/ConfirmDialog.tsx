import { useEffect, useId, useRef, type KeyboardEvent } from 'react'
import { dangerButton, secondaryButton } from './ui'

interface ConfirmDialogProps {
  title: string
  message: string
  confirmLabel: string
  busy?: boolean
  onConfirm: () => void
  onCancel: () => void
}

/** Accessible modal confirmation (replaces window.confirm). Render it only while open. */
export function ConfirmDialog({ title, message, confirmLabel, busy, onConfirm, onCancel }: ConfirmDialogProps) {
  const titleId = useId()
  const descId = useId()
  const cancelRef = useRef<HTMLButtonElement>(null)
  const confirmRef = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null
    cancelRef.current?.focus()
    return () => previous?.focus()
  }, [])

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key === 'Escape') {
      e.stopPropagation()
      onCancel()
      return
    }
    if (e.key === 'Tab') {
      // Keep focus inside the two-button dialog.
      const first = cancelRef.current
      const last = confirmRef.current
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault()
        last?.focus()
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault()
        first?.focus()
      }
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4" onKeyDown={onKeyDown}>
      <div
        role="alertdialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={descId}
        className="w-full max-w-md rounded-xl border border-gray-600 bg-gray-800 p-6 shadow-2xl"
      >
        <h2 id={titleId} className="mb-2 text-xl font-bold text-white">
          {title}
        </h2>
        <p id={descId} className="mb-6 text-gray-300">
          {message}
        </p>
        <div className="flex justify-end gap-3">
          <button type="button" ref={cancelRef} onClick={onCancel} disabled={busy} className={secondaryButton}>
            Cancel
          </button>
          <button type="button" ref={confirmRef} onClick={onConfirm} disabled={busy} className={dangerButton}>
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  )
}
