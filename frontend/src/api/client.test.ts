import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, apiFetch, parseRetryAfter } from './client'
import { mockApi } from '../test/mockApi'

afterEach(() => vi.unstubAllGlobals())

describe('apiFetch', () => {
  it('sends same-origin credentials and parses JSON', async () => {
    const api = mockApi({ 'GET /api/notes': { body: [{ id: '1' }] } })
    await expect(apiFetch('/notes')).resolves.toEqual([{ id: '1' }])
    const call = api.calls[0]
    expect(call.path).toBe('/api/notes')
    expect(call.init.credentials).toBe('same-origin')
    expect(call.init.headers).toMatchObject({ Accept: 'application/json' })
  })

  it('sends JSON bodies with a content type', async () => {
    const api = mockApi({ 'POST /api/notes': { status: 201, body: { id: '1' } } })
    await apiFetch('/notes', { method: 'POST', body: { title: 't' } })
    expect(api.calls[0].body).toEqual({ title: 't' })
    expect(api.calls[0].init.headers).toMatchObject({ 'Content-Type': 'application/json' })
  })

  it('returns undefined for 204', async () => {
    mockApi({ 'DELETE /api/notes/1': { status: 204 } })
    await expect(apiFetch('/notes/1', { method: 'DELETE' })).resolves.toBeUndefined()
  })

  it('parses structured errors including fields and Retry-After', async () => {
    mockApi({
      'POST /api/auth/login': {
        status: 429,
        body: { error: 'too_many_attempts', message: 'locked', fields: { email: 'bad' } },
        headers: { 'Retry-After': '120' },
      },
    })
    const err = await apiFetch('/auth/login', { method: 'POST', body: {} }).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err).toMatchObject({
      status: 429,
      code: 'too_many_attempts',
      message: 'locked',
      fields: { email: 'bad' },
      retryAfter: 120,
    })
  })

  it('falls back to a generic error for non-JSON failures', async () => {
    mockApi({ 'GET /api/notes': { status: 502 } })
    await expect(apiFetch('/notes')).rejects.toMatchObject({ status: 502, code: 'unknown_error' })
  })

  it('maps network failures to ApiError(network_error)', async () => {
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))
    await expect(apiFetch('/notes')).rejects.toMatchObject({ status: 0, code: 'network_error' })
  })

  it('rejects an empty 200 body as an invalid response', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 200 })))
    await expect(apiFetch('/notes')).rejects.toMatchObject({ code: 'invalid_response' })
  })
})

describe('parseRetryAfter', () => {
  it('parses seconds, HTTP dates and garbage', () => {
    const now = Date.parse('2026-01-01T00:00:00Z')
    expect(parseRetryAfter('60')).toBe(60)
    expect(parseRetryAfter('Thu, 01 Jan 2026 00:01:30 GMT', now)).toBe(90)
    expect(parseRetryAfter('soon')).toBeUndefined()
    expect(parseRetryAfter(null)).toBeUndefined()
  })
})
