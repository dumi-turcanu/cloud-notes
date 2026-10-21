/** A failure of the in-browser sign-in step, already worded for the user. */
export class AuthFlowError extends Error {
  /** Stable code, e.g. `invalid_credentials` or `cancelled`. */
  readonly code: string

  constructor(code: string, message: string) {
    super(message)
    this.name = 'AuthFlowError'
    this.code = code
  }
}

export function isAuthFlowError(err: unknown): err is AuthFlowError {
  return err instanceof AuthFlowError
}
