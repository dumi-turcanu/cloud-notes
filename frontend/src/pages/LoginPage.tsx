import { useState, type FormEvent } from 'react'
import { Link, Navigate, useNavigate, useSearchParams } from 'react-router'
import { ErrorBanner } from '../components/ErrorBanner'
import { TextField } from '../components/TextField'
import { primaryButton } from '../components/ui'
import { useAppConfig } from '../context/useAppConfig'
import { useAuth } from '../context/useAuth'
import { isApiError } from '../api/client'
import { describeError, safeRedirect } from '../lib/errors'
import { AuthShell } from './AuthShell'

export function LoginPage() {
  const { status, login } = useAuth()
  const { signup_mode } = useAppConfig()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const from = safeRedirect(params.get('from'))

  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [emailError, setEmailError] = useState<string>()
  const [passwordError, setPasswordError] = useState<string>()
  const [formError, setFormError] = useState<string>()
  const [busy, setBusy] = useState(false)

  if (status === 'authenticated') return <Navigate to={from} replace />

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault()
    setFormError(undefined)
    const trimmedEmail = email.trim()
    const eErr = !trimmedEmail ? 'Email is required.' : !trimmedEmail.includes('@') ? 'Enter a valid email address.' : undefined
    const pErr = !password ? 'Password is required.' : undefined
    setEmailError(eErr)
    setPasswordError(pErr)
    if (eErr || pErr) return

    setBusy(true)
    try {
      await login({ email: trimmedEmail, password })
      void navigate(from, { replace: true })
    } catch (err) {
      if (isApiError(err) && err.code === 'invalid_credentials') {
        setFormError('Invalid email or password.')
      } else {
        setFormError(describeError(err, 'Sign in failed. Please try again.'))
      }
      setBusy(false)
    }
  }

  return (
    <AuthShell title="Sign in">
      <form onSubmit={(e) => void onSubmit(e)} noValidate>
        <ErrorBanner message={formError} />
        <TextField
          label="Email"
          type="email"
          autoComplete="username"
          value={email}
          onChange={setEmail}
          error={emailError}
          disabled={busy}
        />
        <TextField
          label="Password"
          type="password"
          autoComplete="current-password"
          value={password}
          onChange={setPassword}
          error={passwordError}
          disabled={busy}
        />
        <button type="submit" disabled={busy} className={`${primaryButton} w-full`}>
          {busy ? 'Signing in…' : 'Sign in'}
        </button>
      </form>
      {signup_mode !== 'disabled' && (
        <p className="mt-6 text-center text-sm text-gray-400">
          No account yet?{' '}
          <Link to="/register" className="text-blue-400 underline hover:text-blue-300">
            Create one
          </Link>
        </p>
      )}
    </AuthShell>
  )
}
