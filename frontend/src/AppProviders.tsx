import type { ReactNode } from 'react'
import { QueryClientProvider, type QueryClient } from '@tanstack/react-query'
import { AppConfigProvider } from './context/AppConfigProvider'
import { AuthProvider } from './context/AuthProvider'

export function AppProviders({ client, children }: { client: QueryClient; children: ReactNode }) {
  return (
    <QueryClientProvider client={client}>
      <AppConfigProvider>
        <AuthProvider>{children}</AuthProvider>
      </AppConfigProvider>
    </QueryClientProvider>
  )
}
