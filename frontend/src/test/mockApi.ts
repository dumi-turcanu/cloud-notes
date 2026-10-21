import { vi } from 'vitest'
import type { AppConfig, AuthConfig, IdentityPlatformConfig, Note, User } from '../api/types'

export interface MockRequest {
  method: string
  path: string
  body: unknown
  init: RequestInit
}

export interface MockReply {
  status?: number
  body?: unknown
  headers?: Record<string, string>
}

export type MockHandler = MockReply | ((req: MockRequest) => MockReply | Promise<MockReply>)

export interface MockApi {
  calls: MockRequest[]
  /** Calls whose key (`METHOD /path`) matches. */
  callsTo: (key: string) => MockRequest[]
  set: (key: string, handler: MockHandler) => void
}

/** Installs a `fetch` stub routed by `"METHOD /api/path"` keys. Unknown routes answer 404 `not_found`. */
export function mockApi(initial: Record<string, MockHandler> = {}): MockApi {
  const handlers = new Map<string, MockHandler>(Object.entries(initial))
  const calls: MockRequest[] = []

  const fetchMock = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
    const url = new URL(String(input), 'http://localhost')
    const method = (init.method ?? 'GET').toUpperCase()
    const request: MockRequest = {
      method,
      path: url.pathname + url.search,
      body: typeof init.body === 'string' ? (JSON.parse(init.body) as unknown) : undefined,
      init,
    }
    calls.push(request)
    const handler = handlers.get(`${method} ${request.path}`) ?? handlers.get(`${method} ${url.pathname}`)
    const reply: MockReply = handler
      ? typeof handler === 'function'
        ? await handler(request)
        : handler
      : { status: 404, body: { error: 'not_found', message: 'not found' } }
    const status = reply.status ?? 200
    const hasBody = reply.body !== undefined && status !== 204
    return new Response(hasBody ? JSON.stringify(reply.body) : null, {
      status,
      headers: { ...(hasBody ? { 'Content-Type': 'application/json' } : {}), ...reply.headers },
    })
  })
  vi.stubGlobal('fetch', fetchMock)

  return {
    calls,
    callsTo: (key) => calls.filter((c) => `${c.method} ${c.path}` === key),
    set: (key, handler) => void handlers.set(key, handler),
  }
}

export const unauthorized: MockReply = {
  status: 401,
  body: { error: 'unauthorized', message: 'Authentication required.' },
}

export const testUser: User = {
  id: '11111111-1111-4111-8111-111111111111',
  email: 'ann@example.com',
  display_name: 'Ann',
  created_at: '2026-01-01T00:00:00Z',
}

export function makeConfig(overrides: Partial<AppConfig> = {}): AppConfig {
  return {
    signup_mode: 'open',
    features: { public_sharing: false },
    version: 'test',
    ...overrides,
  }
}

export const oidcAuth = (display_name = 'Acme SSO'): AuthConfig => ({ provider: 'oidc', display_name })

export const identityPlatformConfig: IdentityPlatformConfig = {
  api_key: 'public-key',
  auth_domain: 'p.firebaseapp.com',
  project_id: 'p',
  sign_in_methods: ['password'],
}

export const identityPlatformAuth = (overrides: Partial<IdentityPlatformConfig> = {}): AuthConfig => ({
  provider: 'identity-platform',
  identity_platform: { ...identityPlatformConfig, ...overrides },
})

export function makeNote(overrides: Partial<Note> = {}): Note {
  return {
    id: '22222222-2222-4222-8222-222222222222',
    user_id: testUser.id,
    title: 'Groceries',
    content: { body: 'milk\neggs' },
    is_public: false,
    share_uuid: '33333333-3333-4333-8333-333333333333',
    created_at: '2026-01-02T00:00:00Z',
    updated_at: '2026-01-02T00:00:00Z',
    deleted_at: null,
    ...overrides,
  }
}

/** Config + session handlers shared by most page tests. */
export function baseHandlers(
  opts: { config?: Partial<AppConfig>; authenticated?: boolean } = {},
): Record<string, MockHandler> {
  return {
    'GET /api/config': { body: makeConfig(opts.config) },
    'GET /api/auth/me': opts.authenticated ? { body: testUser } : unauthorized,
  }
}
