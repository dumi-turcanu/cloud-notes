import { Link } from 'react-router'

export function NotFoundPage() {
  return (
    <main className="mx-auto mt-24 max-w-md p-4 text-center">
      <h1 className="mb-2 text-3xl font-bold text-white">Page not found</h1>
      <p className="mb-6 text-gray-400">The page you are looking for does not exist.</p>
      <Link to="/" className="text-blue-400 underline hover:text-blue-300">
        Go to your notes
      </Link>
    </main>
  )
}
