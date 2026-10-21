import type { Auth } from 'firebase/auth'
import type { IdentityPlatformConfig } from '../api/types'
import { AuthFlowError } from './errors'

/*
 * GCP Identity Platform sign-in through the Firebase Auth SDK.
 *
 * The SDK is only loaded (dynamic import) when this mode is active, so it is a
 * separate chunk and the default build pays nothing for it. Nothing is
 * persisted: the session is our HttpOnly cookie, so each helper returns a
 * fresh ID token for POST /api/auth/session and then signs out of the SDK.
 */

type AuthModule = typeof import('firebase/auth')

interface Loaded {
  auth: Auth
  mod: AuthModule
}

let loaded: { key: string; promise: Promise<Loaded> } | undefined

/** Initialises the SDK once per configuration and returns its Auth instance. */
export function getIdpAuth(cfg: IdentityPlatformConfig): Promise<Loaded> {
  const key = JSON.stringify([cfg.api_key, cfg.auth_domain, cfg.project_id, cfg.tenant_id ?? ''])
  if (loaded?.key === key) return loaded.promise
  const promise = (async () => {
    const [{ initializeApp }, mod] = await Promise.all([import('firebase/app'), import('firebase/auth')])
    const app = initializeApp(
      { apiKey: cfg.api_key, authDomain: cfg.auth_domain, projectId: cfg.project_id },
      `cloud-notes:${cfg.project_id}:${cfg.tenant_id ?? ''}`,
    )
    // In-memory persistence: no token ever reaches IndexedDB or localStorage.
    const auth = mod.initializeAuth(app, {
      persistence: mod.inMemoryPersistence,
      popupRedirectResolver: mod.browserPopupRedirectResolver,
    })
    if (cfg.tenant_id) auth.tenantId = cfg.tenant_id
    return { auth, mod }
  })()
  loaded = { key, promise }
  // A failed load (offline, blocked chunk) must be retried by the next attempt.
  promise.catch(() => {
    if (loaded?.promise === promise) loaded = undefined
  })
  return promise
}

/** Forgets the initialised SDK (tests). */
export function resetIdpAuth(): void {
  loaded = undefined
}

const MESSAGES: Record<string, [code: string, message: string]> = {
  'auth/invalid-credential': ['invalid_credentials', 'Invalid email or password.'],
  'auth/wrong-password': ['invalid_credentials', 'Invalid email or password.'],
  'auth/user-not-found': ['invalid_credentials', 'Invalid email or password.'],
  'auth/invalid-email': ['invalid_email', 'Enter a valid email address.'],
  'auth/missing-password': ['invalid_credentials', 'Password is required.'],
  'auth/email-already-in-use': ['email_taken', 'An account with this email already exists.'],
  'auth/weak-password': ['weak_password', 'That password is too weak. Choose a longer, less guessable one.'],
  'auth/too-many-requests': ['too_many_requests', 'Too many attempts. Please wait a few minutes and try again.'],
  'auth/user-disabled': ['user_disabled', 'This account has been disabled.'],
  'auth/operation-not-allowed': ['not_allowed', 'This sign-in method is not enabled for the project.'],
  'auth/network-request-failed': ['network_error', 'Cannot reach the sign-in service. Check your connection and try again.'],
  'auth/popup-blocked': ['popup_blocked', 'Your browser blocked the sign-in window. Allow pop-ups for this site and try again.'],
  'auth/popup-closed-by-user': ['cancelled', 'Sign-in was cancelled.'],
  'auth/cancelled-popup-request': ['cancelled', 'Sign-in was cancelled.'],
}

/** Maps a Firebase error to a user-facing AuthFlowError (anything unknown becomes a generic one). */
export function toAuthFlowError(err: unknown, fallback = 'Sign in failed. Please try again.'): AuthFlowError {
  if (err instanceof AuthFlowError) return err
  const code = typeof err === 'object' && err !== null && 'code' in err ? String((err as { code: unknown }).code) : ''
  const known = MESSAGES[code]
  return known ? new AuthFlowError(known[0], known[1]) : new AuthFlowError('unknown', fallback)
}

/** Runs a sign-in, returns its fresh ID token and leaves the SDK signed out. */
async function tokenFrom(cfg: IdentityPlatformConfig, signIn: (l: Loaded) => Promise<{ user: { getIdToken(): Promise<string> } }>): Promise<string> {
  let sdk: Loaded
  try {
    sdk = await getIdpAuth(cfg)
  } catch (err) {
    console.warn('sign-in library failed to load', err instanceof Error ? err.message : err)
    throw new AuthFlowError('load_failed', 'The sign-in service could not be loaded. Check your connection and try again.')
  }
  try {
    const { user } = await signIn(sdk)
    return await user.getIdToken()
  } catch (err) {
    throw toAuthFlowError(err)
  } finally {
    // Our cookie is the session; do not leave the user signed in inside the SDK.
    await sdk.mod.signOut(sdk.auth).catch(() => undefined)
  }
}

export const signInPassword = (cfg: IdentityPlatformConfig, email: string, password: string) =>
  tokenFrom(cfg, ({ auth, mod }) => mod.signInWithEmailAndPassword(auth, email, password))

export const signUpPassword = (cfg: IdentityPlatformConfig, email: string, password: string) =>
  tokenFrom(cfg, ({ auth, mod }) => mod.createUserWithEmailAndPassword(auth, email, password))

export const signInGoogle = (cfg: IdentityPlatformConfig) =>
  tokenFrom(cfg, ({ auth, mod }) => mod.signInWithPopup(auth, new mod.GoogleAuthProvider()))

export async function sendReset(cfg: IdentityPlatformConfig, email: string): Promise<void> {
  let sdk: Loaded
  try {
    sdk = await getIdpAuth(cfg)
  } catch {
    throw new AuthFlowError('load_failed', 'The sign-in service could not be loaded. Check your connection and try again.')
  }
  try {
    await sdk.mod.sendPasswordResetEmail(sdk.auth, email)
  } catch (err) {
    const flow = toAuthFlowError(err, 'Could not send the reset e-mail. Please try again.')
    // Do not reveal which addresses have an account.
    if (flow.code === 'invalid_credentials') return
    throw flow
  }
}
