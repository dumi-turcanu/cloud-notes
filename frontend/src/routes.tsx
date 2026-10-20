import type { RouteObject } from 'react-router'
import { Layout } from './components/Layout'
import { RequireAuth } from './components/RequireAuth'
import { LoginPage } from './pages/LoginPage'
import { NotesPage } from './pages/NotesPage'
import { NotFoundPage } from './pages/NotFoundPage'
import { RegisterPage } from './pages/RegisterPage'
import { SharedNotePage } from './pages/SharedNotePage'
import { TrashPage } from './pages/TrashPage'

export const appRoutes: RouteObject[] = [
  { path: '/login', element: <LoginPage /> },
  { path: '/register', element: <RegisterPage /> },
  { path: '/shared/:shareUuid', element: <SharedNotePage /> },
  {
    element: <RequireAuth />,
    children: [
      {
        element: <Layout />,
        children: [
          { path: '/', element: <NotesPage /> },
          { path: '/trash', element: <TrashPage /> },
        ],
      },
    ],
  },
  { path: '*', element: <NotFoundPage /> },
]
