import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { IdentityPlatformConfig } from '../api/types'
import { isAuthFlowError } from './errors'
import {
  getIdpAuth,
  resetIdpAuth,
  sendReset,
  signInGoogle,
  signInPassword,
  signUpPassword,
  toAuthFlowError,
} from './identityPlatform'

const fb = vi.hoisted(() => ({
  initializeApp: vi.fn((options: unknown, name: string) => ({ options, name })),
  initializeAuth: vi.fn((): { tenantId: string | null } => ({ tenantId: null })),
  signInWithEmailAndPassword: vi.fn(),
  createUserWithEmailAndPassword: vi.fn(),
  signInWithPopup: vi.fn(),
  sendPasswordResetEmail: vi.fn(),
  signOut: vi.fn(() => Promise.resolve()),
  GoogleAuthProvider: class GoogleAuthProvider {},
  inMemoryPersistence: { type: 'NONE' },
  browserPopupRedirectResolver: { resolver: true },
}))

vi.mock('firebase/app', () => ({ initializeApp: fb.initializeApp }))
vi.mock('firebase/auth', () => ({
  initializeAuth: fb.initializeAuth,
  signInWithEmailAndPassword: fb.signInWithEmailAndPassword,
  createUserWithEmailAndPassword: fb.createUserWithEmailAndPassword,
  signInWithPopup: fb.signInWithPopup,
  sendPasswordResetEmail: fb.sendPasswordResetEmail,
  signOut: fb.signOut,
  GoogleAuthProvider: fb.GoogleAuthProvider,
  inMemoryPersistence: fb.inMemoryPersistence,
  browserPopupRedirectResolver: fb.browserPopupRedirectResolver,
}))

const cfg: IdentityPlatformConfig = {
  api_key: 'public-key',
  auth_domain: 'p.firebaseapp.com',
  project_id: 'p',
  sign_in_methods: ['password', 'google'],
}

const credential = (token: string) => ({ user: { getIdToken: () => Promise.resolve(token) } })
const firebaseError = (code: string) => Object.assign(new Error(`Firebase: Error (${code}).`), { code })

beforeEach(() => {
  resetIdpAuth()
  vi.clearAllMocks()
  fb.initializeAuth.mockImplementation(() => ({ tenantId: null }))
  fb.signOut.mockImplementation(() => Promise.resolve())
})

describe('getIdpAuth', () => {
  it('initialises with in-memory persistence and the popup resolver', async () => {
    await getIdpAuth(cfg)
    expect(fb.initializeApp).toHaveBeenCalledWith(
      { apiKey: 'public-key', authDomain: 'p.firebaseapp.com', projectId: 'p' },
      expect.any(String),
    )
    expect(fb.initializeAuth).toHaveBeenCalledWith(expect.anything(), {
      persistence: fb.inMemoryPersistence,
      popupRedirectResolver: fb.browserPopupRedirectResolver,
    })
  })

  it('sets the tenant when configured', async () => {
    const auth = { tenantId: null as string | null }
    fb.initializeAuth.mockImplementation(() => auth)
    await getIdpAuth({ ...cfg, tenant_id: 'tenant-1' })
    expect(auth.tenantId).toBe('tenant-1')
  })

  it('initialises once per configuration', async () => {
    await getIdpAuth(cfg)
    await getIdpAuth(cfg)
    expect(fb.initializeApp).toHaveBeenCalledTimes(1)
    expect(fb.initializeAuth).toHaveBeenCalledTimes(1)
  })

  it('retries after a failed initialisation', async () => {
    fb.initializeAuth.mockImplementationOnce(() => {
      throw new Error('chunk failed')
    })
    await expect(getIdpAuth(cfg)).rejects.toThrow('chunk failed')
    await expect(getIdpAuth(cfg)).resolves.toBeDefined()
  })
})

describe('sign-in helpers', () => {
  it('signInPassword returns the ID token and signs out of the SDK', async () => {
    fb.signInWithEmailAndPassword.mockResolvedValue(credential('id-token-1'))
    await expect(signInPassword(cfg, 'a@example.com', 'secret-password')).resolves.toBe('id-token-1')
    expect(fb.signInWithEmailAndPassword).toHaveBeenCalledWith(expect.anything(), 'a@example.com', 'secret-password')
    expect(fb.signOut).toHaveBeenCalledTimes(1)
  })

  it('signUpPassword creates the account and returns its token', async () => {
    fb.createUserWithEmailAndPassword.mockResolvedValue(credential('id-token-2'))
    await expect(signUpPassword(cfg, 'new@example.com', 'long-enough-password')).resolves.toBe('id-token-2')
    expect(fb.signOut).toHaveBeenCalledTimes(1)
  })

  it('signInGoogle uses a Google popup', async () => {
    fb.signInWithPopup.mockResolvedValue(credential('id-token-3'))
    await expect(signInGoogle(cfg)).resolves.toBe('id-token-3')
    expect(fb.signInWithPopup).toHaveBeenCalledWith(expect.anything(), expect.any(fb.GoogleAuthProvider))
    expect(fb.signOut).toHaveBeenCalledTimes(1)
  })

  it('signs out even when sign-in fails, and maps the error', async () => {
    fb.signInWithEmailAndPassword.mockRejectedValue(firebaseError('auth/invalid-credential'))
    const err = await signInPassword(cfg, 'a@example.com', 'x').catch((e: unknown) => e)
    expect(isAuthFlowError(err) && err.code).toBe('invalid_credentials')
    expect(isAuthFlowError(err) && err.message).toBe('Invalid email or password.')
    expect(fb.signOut).toHaveBeenCalledTimes(1)
  })

  it('does not fail the sign-in when signOut fails', async () => {
    fb.signInWithEmailAndPassword.mockResolvedValue(credential('t'))
    fb.signOut.mockRejectedValue(new Error('boom'))
    await expect(signInPassword(cfg, 'a@example.com', 'x')).resolves.toBe('t')
  })

  it('reports a load failure in plain words', async () => {
    fb.initializeAuth.mockImplementationOnce(() => {
      throw new Error('Failed to fetch dynamically imported module')
    })
    vi.spyOn(console, 'warn').mockImplementation(() => undefined)
    const err = await signInPassword(cfg, 'a@example.com', 'x').catch((e: unknown) => e)
    expect(isAuthFlowError(err) && err.code).toBe('load_failed')
    expect(String((err as Error).message)).not.toContain('dynamically imported')
  })
})

describe('sendReset', () => {
  it('sends the e-mail', async () => {
    fb.sendPasswordResetEmail.mockResolvedValue(undefined)
    await sendReset(cfg, 'a@example.com')
    expect(fb.sendPasswordResetEmail).toHaveBeenCalledWith(expect.anything(), 'a@example.com')
  })

  it('does not reveal unknown addresses', async () => {
    fb.sendPasswordResetEmail.mockRejectedValue(firebaseError('auth/user-not-found'))
    await expect(sendReset(cfg, 'nobody@example.com')).resolves.toBeUndefined()
  })

  it('surfaces other failures', async () => {
    fb.sendPasswordResetEmail.mockRejectedValue(firebaseError('auth/too-many-requests'))
    await expect(sendReset(cfg, 'a@example.com')).rejects.toMatchObject({ code: 'too_many_requests' })
  })
})

describe('toAuthFlowError', () => {
  it.each([
    ['auth/invalid-credential', 'invalid_credentials'],
    ['auth/email-already-in-use', 'email_taken'],
    ['auth/weak-password', 'weak_password'],
    ['auth/too-many-requests', 'too_many_requests'],
    ['auth/network-request-failed', 'network_error'],
    ['auth/popup-closed-by-user', 'cancelled'],
    ['auth/popup-blocked', 'popup_blocked'],
  ])('maps %s to %s', (firebaseCode, code) => {
    const err = toAuthFlowError(firebaseError(firebaseCode))
    expect(err.code).toBe(code)
    expect(err.message).not.toContain('auth/')
  })

  it('never leaks unknown Firebase text', () => {
    const err = toAuthFlowError(firebaseError('auth/some-new-code'), 'Sign in failed.')
    expect(err.code).toBe('unknown')
    expect(err.message).toBe('Sign in failed.')
    expect(toAuthFlowError('weird').message).toBe('Sign in failed. Please try again.')
  })
})
