const API_PREFIX = '/api'

/** Error returned by the API (or synthesised for network / malformed responses). */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly fields: Record<string, string>
  /** Seconds from the `Retry-After` header, if the server sent one. */
  readonly retryAfter: number | undefined

  constructor(init: {
    status: number
    code: string
    message: string
    fields?: Record<string, string>
    retryAfter?: number
  }) {
    super(init.message)
    this.name = 'ApiError'
    this.status = init.status
    this.code = init.code
    this.fields = init.fields ?? {}
    this.retryAfter = init.retryAfter
  }
}

export function isApiError(err: unknown): err is ApiError {
  return err instanceof ApiError
}

export function parseRetryAfter(value: string | null, now: number = Date.now()): number | undefined {
  if (!value) return undefined
  const trimmed = value.trim()
  if (/^\d+$/.test(trimmed)) return Number(trimmed)
  const date = Date.parse(trimmed)
  if (Number.isNaN(date)) return undefined
  return Math.max(0, Math.ceil((date - now) / 1000))
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function toFields(value: unknown): Record<string, string> {
  if (!isRecord(value)) return {}
  const out: Record<string, string> = {}
  for (const [key, entry] of Object.entries(value)) {
    if (typeof entry === 'string') out[key] = entry
  }
  return out
}

async function readJson(response: Response): Promise<unknown> {
  const text = await response.text()
  if (!text) return undefined
  try {
    return JSON.parse(text)
  } catch {
    return undefined
  }
}

async function toApiError(response: Response): Promise<ApiError> {
  const body = await readJson(response)
  const record = isRecord(body) ? body : {}
  return new ApiError({
    status: response.status,
    code: typeof record.error === 'string' ? record.error : 'unknown_error',
    message:
      typeof record.message === 'string' ? record.message : `Request failed (${response.status})`,
    fields: toFields(record.fields),
    retryAfter: parseRetryAfter(response.headers.get('Retry-After')),
  })
}

export interface ApiRequest {
  method?: 'GET' | 'POST' | 'PUT' | 'DELETE'
  body?: unknown
  signal?: AbortSignal
}

/**
 * Calls the same-origin API. `path` is relative to `/api` (e.g. `/notes`).
 * Authentication is carried exclusively by the HttpOnly session cookie.
 */
export async function apiFetch<T>(path: string, request: ApiRequest = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  let body: string | undefined
  if (request.body !== undefined) {
    headers['Content-Type'] = 'application/json'
    body = JSON.stringify(request.body)
  }

  let response: Response
  try {
    response = await fetch(`${API_PREFIX}${path}`, {
      method: request.method ?? 'GET',
      credentials: 'same-origin',
      headers,
      body,
      signal: request.signal,
    })
  } catch (err) {
    if (err instanceof DOMException && err.name === 'AbortError') throw err
    throw new ApiError({
      status: 0,
      code: 'network_error',
      message: 'Cannot reach the server. Check your connection and try again.',
    })
  }

  if (!response.ok) throw await toApiError(response)
  if (response.status === 204) return undefined as T

  const data = await readJson(response)
  if (data === undefined) {
    throw new ApiError({
      status: response.status,
      code: 'invalid_response',
      message: 'The server returned an unexpected response.',
    })
  }
  return data as T
}
