import { apiFetch } from './client'
import type { LoginInput, RegisterInput, User } from './types'

export const login = (input: LoginInput) =>
  apiFetch<User>('/auth/login', { method: 'POST', body: input })

export const register = (input: RegisterInput) =>
  apiFetch<User>('/auth/register', { method: 'POST', body: input })

export const logout = () => apiFetch<void>('/auth/logout', { method: 'POST' })

export const me = (signal?: AbortSignal) => apiFetch<User>('/auth/me', { signal })
