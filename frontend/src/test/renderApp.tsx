import { render } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router'
import { AppProviders } from '../AppProviders'
import { createAppQueryClient } from '../lib/queryClient'
import { appRoutes } from '../routes'

/** Renders the whole app (real providers + real routes) at `path`. */
export function renderApp(path: string) {
  const client = createAppQueryClient()
  const router = createMemoryRouter(appRoutes, { initialEntries: [path] })
  const utils = render(
    <AppProviders client={client}>
      <RouterProvider router={router} />
    </AppProviders>,
  )
  return { ...utils, router, client }
}
