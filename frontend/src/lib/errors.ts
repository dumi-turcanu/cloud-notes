import { isApiError } from '../api/client'
import { isAuthFlowError } from '../auth/errors'

/** Human readable text for any error thrown by the API layer. */
export function describeError(err: unknown, fallback = 'Something went wrong. Please try again.'): string {
  if (isAuthFlowError(err)) return err.message
  if (isApiError(err)) {
    if (err.code === 'account_conflict') return ACCOUNT_CONFLICT_MESSAGE
    if (err.code === 'too_many_attempts') return lockoutMessage(err.retryAfter)
    if (err.code === 'too_many_requests') return 'Too many requests. Please wait a minute and try again.'
    return err.message || fallback
  }
  return fallback
}

export const ACCOUNT_CONFLICT_MESSAGE = 'This email address is already used by another account.'

/** Banner text for the `?error=` codes the OIDC callback redirects to /login with. */
const LOGIN_REDIRECT_ERRORS: Record<string, string> = {
  oidc_cancelled: 'Sign-in was cancelled.',
  oidc_failed: 'Sign-in failed. Please try again.',
  account_conflict: ACCOUNT_CONFLICT_MESSAGE,
  email_claim_missing: 'Your identity provider did not return a verified email address.',
}

/** The message for a known `?error=` code, or undefined: unknown values are never echoed. */
export function loginRedirectError(code: string | null): string | undefined {
  return code !== null && Object.hasOwn(LOGIN_REDIRECT_ERRORS, code) ? LOGIN_REDIRECT_ERRORS[code] : undefined
}

export function lockoutMessage(retryAfterSeconds: number | undefined): string {
  if (retryAfterSeconds === undefined) {
    return 'Too many failed attempts. Please try again later.'
  }
  const minutes = Math.max(1, Math.ceil(retryAfterSeconds / 60))
  return `Too many failed attempts. Try again in ${minutes} ${minutes === 1 ? 'minute' : 'minutes'}.`
}

/** Length in Unicode code points, matching how the backend counts characters. */
export function runeLength(value: string): number {
  return Array.from(value).length
}

/** Only allow same-site absolute paths as post-login redirect targets. */
export function safeRedirect(target: string | null): string {
  if (!target || !target.startsWith('/') || target.startsWith('//') || target.startsWith('/\\')) return '/'
  return target
}
