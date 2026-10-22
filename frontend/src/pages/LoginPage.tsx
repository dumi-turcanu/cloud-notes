import { useState, type FormEvent } from 'react'
import { Link, Navigate, useNavigate, useSearchParams } from 'react-router'
import { ErrorBanner } from '../components/ErrorBanner'
import { TextField } from '../components/TextField'
import { primaryButton, secondaryButton } from '../components/ui'
import { useAppConfig } from '../context/useAppConfig'
import { useAuth } from '../context/useAuth'
import { isApiError } from '../api/client'
import type { IdentityPlatformConfig } from '../api/types'
import { isAuthFlowError } from '../auth/errors'
import { sendReset, signInGoogle, signInPassword } from '../auth/identityPlatform'
import { authConfigOf } from '../lib/authConfig'
import { describeError, loginRedirectError, safeRedirect } from '../lib/errors'
import { AuthShell } from './AuthShell'

/** Where the OIDC flow starts: a plain navigation, because the callback comes back as a redirect. */
const oidcLoginPath = (from: string) => `/api/auth/oidc/login?from=${encodeURIComponent(from)}`

export function LoginPage() {
  const { status, login, loginWithIdToken } = useAuth()
  const config = useAppConfig()
  const auth = authConfigOf(config)
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const from = safeRedirect(params.get('from'))
  const redirectError = loginRedirectError(params.get('error'))

  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [emailError, setEmailError] = useState<string>()
  const [passwordError, setPasswordError] = useState<string>()
  const [formError, setFormError] = useState<string>()
  const [notice, setNotice] = useState<string>()
  const [busy, setBusy] = useState(false)

  if (status === 'authenticated') return <Navigate to={from} replace />

  const idp = auth.provider === 'identity-platform' ? auth.identity_platform : undefined
  const passwordEnabled = auth.provider === 'local' || (idp?.sign_in_methods.includes('password') ?? false)
  const googleEnabled = idp?.sign_in_methods.includes('google') ?? false
  const showSignUpLink =
    auth.provider === 'identity-platform' ? passwordEnabled : auth.provider === 'local' && config.signup_mode !== 'disabled'

  const validate = (): string | undefined => {
    const trimmedEmail = email.trim()
    const eErr = !trimmedEmail ? 'Email is required.' : !trimmedEmail.includes('@') ? 'Enter a valid email address.' : undefined
    const pErr = !password ? 'Password is required.' : undefined
    setEmailError(eErr)
    setPasswordError(pErr)
    return eErr || pErr ? undefined : trimmedEmail
  }

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault()
    setFormError(undefined)
    setNotice(undefined)
    const trimmedEmail = validate()
    if (!trimmedEmail) return

    setBusy(true)
    try {
      if (idp) {
        await loginWithIdToken(await signInPassword(idp, trimmedEmail, password))
      } else {
        await login({ email: trimmedEmail, password })
      }
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

  const onGoogle = async (cfg: IdentityPlatformConfig) => {
    setFormError(undefined)
    setNotice(undefined)
    setBusy(true)
    try {
      await loginWithIdToken(await signInGoogle(cfg))
      void navigate(from, { replace: true })
    } catch (err) {
      // Closing the pop-up is not an error worth a banner.
      if (!(isAuthFlowError(err) && err.code === 'cancelled')) {
        setFormError(describeError(err, 'Sign in failed. Please try again.'))
      }
      setBusy(false)
    }
  }

  const onForgotPassword = async (cfg: IdentityPlatformConfig) => {
    setFormError(undefined)
    setNotice(undefined)
    const trimmedEmail = email.trim()
    if (!trimmedEmail || !trimmedEmail.includes('@')) {
      setEmailError('Enter your email address first, then ask for a reset link.')
      return
    }
    setEmailError(undefined)
    setBusy(true)
    try {
      await sendReset(cfg, trimmedEmail)
      setNotice('If an account exists for this address, a password reset link is on its way.')
    } catch (err) {
      setFormError(describeError(err, 'Could not send the reset email. Please try again.'))
    }
    setBusy(false)
  }

  if (auth.provider === 'oidc') {
    return (
      <AuthShell title="Sign in">
        <ErrorBanner message={redirectError} />
        <a href={oidcLoginPath(from)} className={`${primaryButton} block w-full text-center`}>
          Sign in with {auth.display_name || 'Single sign-on'}
        </a>
      </AuthShell>
    )
  }

  return (
    <AuthShell title="Sign in">
      <ErrorBanner message={formError ?? redirectError} />
      {notice && (
        <p role="status" className="mb-6 rounded-lg border border-green-600 bg-green-900/40 p-4 text-sm text-green-100">
          {notice}
        </p>
      )}
      {passwordEnabled && (
        <form onSubmit={(e) => void onSubmit(e)} noValidate>
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
          {idp && (
            <p className="mt-4 text-center text-sm">
              <button
                type="button"
                disabled={busy}
                onClick={() => void onForgotPassword(idp)}
                className="cursor-pointer text-blue-400 underline hover:text-blue-300 disabled:cursor-not-allowed disabled:opacity-60"
              >
                Forgot password?
              </button>
            </p>
          )}
        </form>
      )}
      {idp && googleEnabled && (
        <div className={passwordEnabled ? 'mt-6 border-t border-gray-700 pt-6' : undefined}>
          <button type="button" disabled={busy} onClick={() => void onGoogle(idp)} className={`${secondaryButton} w-full`}>
            Continue with Google
          </button>
        </div>
      )}
      {showSignUpLink && (
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
