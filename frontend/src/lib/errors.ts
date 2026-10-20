import { isApiError } from '../api/client'

/** Human readable text for any error thrown by the API layer. */
export function describeError(err: unknown, fallback = 'Something went wrong. Please try again.'): string {
  if (isApiError(err)) {
    if (err.code === 'too_many_attempts') return lockoutMessage(err.retryAfter)
    if (err.code === 'too_many_requests') return 'Too many requests. Please wait a minute and try again.'
    return err.message || fallback
  }
  return fallback
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
