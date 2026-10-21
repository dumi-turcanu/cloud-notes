import { createContext } from 'react'
import type { LoginInput, RegisterInput, User } from '../api/types'

export type AuthStatus = 'loading' | 'authenticated' | 'anonymous'

export interface AuthContextValue {
  status: AuthStatus
  user: User | null
  login: (input: LoginInput) => Promise<User>
  register: (input: RegisterInput) => Promise<User>
  /** Signs in with an Identity Platform ID token (AUTH_PROVIDER=identity-platform). */
  loginWithIdToken: (idToken: string) => Promise<User>
  logout: () => Promise<void>
}

export const AuthContext = createContext<AuthContextValue | null>(null)
