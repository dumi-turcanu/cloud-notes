import type { AppConfig, AuthConfig } from '../api/types'

/** The auth section of the app config; an API that predates it is a `local` one. */
export function authConfigOf(config: AppConfig): AuthConfig {
  return config.auth ?? { provider: 'local' }
}
