import { Navigate, Outlet, useLocation } from 'react-router'
import { useAuth } from '../context/useAuth'

/** Layout route: renders children only for authenticated users, otherwise redirects to /login?from=… */
export function RequireAuth() {
  const { status } = useAuth()
  const location = useLocation()

  if (status === 'loading') {
    return <p role="status" className="p-8 text-center text-gray-400">Loading…</p>
  }
  if (status === 'anonymous') {
    const from = location.pathname + location.search
    return <Navigate to={`/login?from=${encodeURIComponent(from)}`} replace />
  }
  return <Outlet />
}
