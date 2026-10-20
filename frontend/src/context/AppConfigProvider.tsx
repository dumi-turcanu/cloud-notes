import type { ReactNode } from 'react'
import { useQuery } from '@tanstack/react-query'
import { getConfig } from '../api/config'
import { AppConfigContext } from './appConfigContext'

export function AppConfigProvider({ children }: { children: ReactNode }) {
  const { data, isPending, isError, refetch, isFetching } = useQuery({
    queryKey: ['config'],
    queryFn: ({ signal }) => getConfig(signal),
    staleTime: Infinity,
  })

  if (isPending && !isError) {
    return <p role="status" className="p-8 text-center text-gray-400">Loading…</p>
  }
  if (isError || !data) {
    return (
      <div role="alert" className="mx-auto mt-24 max-w-md rounded-xl border border-red-500 bg-red-900/40 p-6 text-center">
        <h1 className="mb-2 text-xl font-bold text-red-100">Cloud Notes is unavailable</h1>
        <p className="mb-4 text-red-200">The application could not reach its server.</p>
        <button
          type="button"
          onClick={() => void refetch()}
          disabled={isFetching}
          className="rounded-lg bg-gray-700 px-4 py-2 text-white hover:bg-gray-600 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-400 disabled:opacity-60"
        >
          Try again
        </button>
      </div>
    )
  }
  return <AppConfigContext.Provider value={data}>{children}</AppConfigContext.Provider>
}
