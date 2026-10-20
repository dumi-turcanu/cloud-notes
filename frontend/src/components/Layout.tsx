import { useState } from 'react'
import { NavLink, Outlet } from 'react-router'
import { useAuth } from '../context/useAuth'
import { focusRing, secondaryButton } from './ui'

const navClass = ({ isActive }: { isActive: boolean }) =>
  `rounded-lg px-3 py-1.5 text-sm font-medium ${focusRing} ${
    isActive ? 'bg-gray-700 text-white' : 'text-gray-300 hover:bg-gray-800'
  }`

export function Layout() {
  const { user, logout } = useAuth()
  const [signingOut, setSigningOut] = useState(false)

  const onSignOut = async () => {
    setSigningOut(true)
    await logout()
  }

  return (
    <div className="mx-auto w-full max-w-4xl px-4 py-8">
      <header className="mb-8 flex flex-wrap items-center justify-between gap-4 border-b border-gray-700 pb-4">
        <div>
          <p className="text-3xl font-bold text-blue-400">Cloud Notes</p>
          <p className="text-sm text-gray-400">Signed in as {user?.display_name ?? user?.email}</p>
        </div>
        <nav aria-label="Main" className="flex items-center gap-2">
          <NavLink to="/" end className={navClass}>
            Notes
          </NavLink>
          <NavLink to="/trash" className={navClass}>
            Trash
          </NavLink>
          <button type="button" onClick={() => void onSignOut()} disabled={signingOut} className={secondaryButton}>
            Sign out
          </button>
        </nav>
      </header>
      <main>
        <Outlet />
      </main>
    </div>
  )
}
