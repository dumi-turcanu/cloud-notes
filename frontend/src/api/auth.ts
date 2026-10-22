import { apiFetch } from './client'
import type { LoginInput, RegisterInput, User } from './types'

export const login = (input: LoginInput) =>
  apiFetch<User>('/auth/login', { method: 'POST', body: input })

export const register = (input: RegisterInput) =>
  apiFetch<User>('/auth/register', { method: 'POST', body: input })

/** Exchanges a fresh GCP Identity Platform ID token for the session cookie. */
export const createSession = (idToken: string) =>
  apiFetch<User>('/auth/session', { method: 'POST', body: { id_token: idToken } })

/** Resolves to `{ logout_url }` when the identity provider's own session should be ended too. */
export const logout = () => apiFetch<{ logout_url?: string } | void>('/auth/logout', { method: 'POST' })

export const me = (signal?: AbortSignal) => apiFetch<User>('/auth/me', { signal })
