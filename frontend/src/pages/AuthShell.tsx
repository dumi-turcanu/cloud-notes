import type { ReactNode } from 'react'
import { cardClass } from '../components/ui'

export function AuthShell({ title, children }: { title: string; children: ReactNode }) {
  return (
    <main className="mx-auto flex min-h-screen w-full max-w-md items-center px-4 py-8">
      <div className={`${cardClass} w-full`}>
        <p className="mb-1 text-center text-sm font-semibold uppercase tracking-wide text-blue-400">Cloud Notes</p>
        <h1 className="mb-6 text-center text-2xl font-bold text-white">{title}</h1>
        {children}
      </div>
    </main>
  )
}
