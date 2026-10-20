import { useId, type ChangeEvent, type HTMLInputTypeAttribute } from 'react'
import { inputClass } from './ui'

interface TextFieldProps {
  label: string
  value: string
  onChange: (value: string) => void
  type?: HTMLInputTypeAttribute
  autoComplete?: string
  hint?: string
  error?: string
  required?: boolean
  disabled?: boolean
  maxLength?: number
  multiline?: boolean
}

export function TextField({
  label,
  value,
  onChange,
  type = 'text',
  autoComplete,
  hint,
  error,
  required,
  disabled,
  maxLength,
  multiline,
}: TextFieldProps) {
  const id = useId()
  const hintId = `${id}-hint`
  const errorId = `${id}-error`
  const describedBy = [hint ? hintId : null, error ? errorId : null].filter(Boolean).join(' ') || undefined
  const common = {
    id,
    value,
    required,
    disabled,
    maxLength,
    'aria-invalid': error ? true : undefined,
    'aria-describedby': describedBy,
    className: inputClass,
  } as const
  const handle = (e: ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) => onChange(e.target.value)

  return (
    <div className="mb-4">
      <label htmlFor={id} className="mb-1 block text-sm font-medium text-gray-300">
        {label}
      </label>
      {multiline ? (
        <textarea {...common} rows={5} onChange={handle} />
      ) : (
        <input {...common} type={type} autoComplete={autoComplete} onChange={handle} />
      )}
      {hint && (
        <p id={hintId} className="mt-1 text-xs text-gray-400">
          {hint}
        </p>
      )}
      {error && (
        <p id={errorId} className="mt-1 text-sm text-red-300">
          {error}
        </p>
      )}
    </div>
  )
}
