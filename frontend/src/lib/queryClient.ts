import { MutationCache, QueryCache, QueryClient } from '@tanstack/react-query'
import { isApiError } from '../api/client'

const listeners = new WeakMap<QueryClient, Set<() => void>>()

/** Creates the app-wide QueryClient; any 401 from a query or mutation notifies `onUnauthorized` listeners. */
export function createAppQueryClient(): QueryClient {
  const subscribers = new Set<() => void>()
  const handleError = (error: unknown) => {
    if (isApiError(error) && error.status === 401) subscribers.forEach((fn) => fn())
  }
  const client = new QueryClient({
    queryCache: new QueryCache({ onError: handleError }),
    mutationCache: new MutationCache({ onError: handleError }),
    defaultOptions: { queries: { retry: false } },
  })
  listeners.set(client, subscribers)
  return client
}

/** Registers a 401 listener on a client made by `createAppQueryClient`; returns an unsubscribe function. */
export function onUnauthorized(client: QueryClient, fn: () => void): () => void {
  const subscribers = listeners.get(client)
  if (!subscribers) return () => {}
  subscribers.add(fn)
  return () => subscribers.delete(fn)
}

export const notesKey = ['notes'] as const
export const trashKey = ['notes', 'trash'] as const
