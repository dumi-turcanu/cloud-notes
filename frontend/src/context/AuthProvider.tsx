import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import * as authApi from '../api/auth'
import type { LoginInput, RegisterInput, User } from '../api/types'
import { notesKey, onUnauthorized } from '../lib/queryClient'
import { AuthContext, type AuthContextValue, type AuthStatus } from './authContext'

/** Only ever navigate to http(s) URLs: the value comes from an API response. */
function isHttpUrl(value: string): boolean {
  try {
    const { protocol } = new URL(value)
    return protocol === 'https:' || protocol === 'http:'
  } catch {
    return false
  }
}

interface AuthState {
  status: AuthStatus
  user: User | null
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient()
  const [state, setState] = useState<AuthState>({ status: 'loading', user: null })

  const setAnonymous = useCallback(() => {
    setState({ status: 'anonymous', user: null })
    // Never keep one user's notes around for the next person on this browser.
    queryClient.removeQueries({ queryKey: notesKey })
  }, [queryClient])

  // Bootstrap from the session cookie.
  useEffect(() => {
    const controller = new AbortController()
    authApi
      .me(controller.signal)
      .then((user) => setState({ status: 'authenticated', user }))
      .catch((err: unknown) => {
        if (controller.signal.aborted) return
        console.warn('session check failed', err instanceof Error ? err.message : err)
        setState({ status: 'anonymous', user: null })
      })
    return () => controller.abort()
  }, [])

  // Any 401 from a protected call means the session is gone.
  useEffect(() => onUnauthorized(queryClient, setAnonymous), [queryClient, setAnonymous])

  const login = useCallback(async (input: LoginInput) => {
    const user = await authApi.login(input)
    setState({ status: 'authenticated', user })
    return user
  }, [])

  const register = useCallback(async (input: RegisterInput) => {
    const user = await authApi.register(input)
    setState({ status: 'authenticated', user })
    return user
  }, [])

  const loginWithIdToken = useCallback(async (idToken: string) => {
    const user = await authApi.createSession(idToken)
    setState({ status: 'authenticated', user })
    return user
  }, [])

  const logout = useCallback(async () => {
    let logoutUrl: string | undefined
    try {
      const result = await authApi.logout()
      logoutUrl = result?.logout_url
    } catch (err) {
      console.warn('logout request failed', err instanceof Error ? err.message : err)
    }
    setAnonymous()
    // With single sign-on the provider has a session of its own: end it too.
    if (logoutUrl && isHttpUrl(logoutUrl)) window.location.assign(logoutUrl)
  }, [setAnonymous])

  const value = useMemo<AuthContextValue>(
    () => ({ ...state, login, register, loginWithIdToken, logout }),
    [state, login, register, loginWithIdToken, logout],
  )
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}
