export function ErrorBanner({ message }: { message: string | null | undefined }) {
  if (!message) return null
  return (
    <div role="alert" className="mb-6 rounded-lg border border-red-500 bg-red-900/50 p-4 text-red-200">
      <p className="font-bold">Error</p>
      <p className="text-sm">{message}</p>
    </div>
  )
}
