import { useState, type FormEvent } from 'react'
import { Link, Navigate, useNavigate } from 'react-router'
import { isApiError } from '../api/client'
import { ErrorBanner } from '../components/ErrorBanner'
import { TextField } from '../components/TextField'
import { primaryButton } from '../components/ui'
import { useAppConfig } from '../context/useAppConfig'
import { useAuth } from '../context/useAuth'
import { describeError, runeLength } from '../lib/errors'
import { AuthShell } from './AuthShell'

type FieldErrors = Partial<Record<'email' | 'password' | 'display_name' | 'invite_code', string>>

export function RegisterPage() {
  const { status, register } = useAuth()
  const { signup_mode } = useAppConfig()
  const navigate = useNavigate()

  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [inviteCode, setInviteCode] = useState('')
  const [errors, setErrors] = useState<FieldErrors>({})
  const [formError, setFormError] = useState<string>()
  const [busy, setBusy] = useState(false)

  if (status === 'authenticated') return <Navigate to="/" replace />
  if (signup_mode === 'disabled') return <Navigate to="/login" replace />

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault()
    setFormError(undefined)
    const trimmedEmail = email.trim()
    const next: FieldErrors = {}
    if (!trimmedEmail || !trimmedEmail.includes('@')) next.email = 'Enter a valid email address.'
    const length = runeLength(password)
    if (length < 12) next.password = 'Password must be at least 12 characters.'
    else if (length > 128) next.password = 'Password must be at most 128 characters.'
    else if (password === trimmedEmail) next.password = 'Password must not be the same as your email.'
    if (signup_mode === 'invite' && !inviteCode.trim()) next.invite_code = 'An invite code is required.'
    setErrors(next)
    if (Object.keys(next).length > 0) return

    setBusy(true)
    try {
      await register({
        email: trimmedEmail,
        password,
        ...(displayName.trim() ? { display_name: displayName.trim() } : {}),
        ...(signup_mode === 'invite' ? { invite_code: inviteCode.trim() } : {}),
      })
      void navigate('/', { replace: true })
    } catch (err) {
      if (isApiError(err) && err.code === 'validation_failed') {
        setErrors({
          email: err.fields.email,
          password: err.fields.password,
          display_name: err.fields.display_name,
          invite_code: err.fields.invite_code,
        })
        if (Object.keys(err.fields).length === 0) setFormError(err.message)
      } else if (isApiError(err) && err.code === 'email_taken') {
        setErrors({ email: 'An account with this email already exists.' })
      } else if (isApiError(err) && err.code === 'invalid_invite_code') {
        setErrors({ invite_code: 'This invite code is not valid.' })
      } else {
        setFormError(describeError(err, 'Registration failed. Please try again.'))
      }
      setBusy(false)
    }
  }

  return (
    <AuthShell title="Create your account">
      <form onSubmit={(e) => void onSubmit(e)} noValidate>
        <ErrorBanner message={formError} />
        <TextField
          label="Email"
          type="email"
          autoComplete="username"
          value={email}
          onChange={setEmail}
          error={errors.email}
          disabled={busy}
        />
        <TextField
          label="Display name (optional)"
          autoComplete="nickname"
          value={displayName}
          onChange={setDisplayName}
          error={errors.display_name}
          maxLength={100}
          disabled={busy}
        />
        <TextField
          label="Password"
          type="password"
          autoComplete="new-password"
          value={password}
          onChange={setPassword}
          hint="At least 12 characters."
          error={errors.password}
          disabled={busy}
        />
        {signup_mode === 'invite' && (
          <TextField
            label="Invite code"
            autoComplete="off"
            value={inviteCode}
            onChange={setInviteCode}
            error={errors.invite_code}
            disabled={busy}
          />
        )}
        <button type="submit" disabled={busy} className={`${primaryButton} w-full`}>
          {busy ? 'Creating account…' : 'Create account'}
        </button>
      </form>
      <p className="mt-6 text-center text-sm text-gray-400">
        Already have an account?{' '}
        <Link to="/login" className="text-blue-400 underline hover:text-blue-300">
          Sign in
        </Link>
      </p>
    </AuthShell>
  )
}
