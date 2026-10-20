export const focusRing =
  'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-blue-400'

export const inputClass =
  'w-full rounded-lg border border-gray-600 bg-gray-900 p-3 text-white placeholder-gray-500 focus:outline-2 focus:outline-blue-500 aria-[invalid=true]:border-red-500'

export const primaryButton = `cursor-pointer rounded-lg bg-blue-600 px-5 py-2 font-semibold text-white transition hover:bg-blue-500 disabled:cursor-not-allowed disabled:bg-gray-600 ${focusRing}`

export const secondaryButton = `cursor-pointer rounded-lg bg-gray-700 px-4 py-2 text-white transition hover:bg-gray-600 disabled:cursor-not-allowed disabled:opacity-60 ${focusRing}`

export const dangerButton = `cursor-pointer rounded-lg bg-red-700 px-4 py-2 text-white transition hover:bg-red-600 disabled:cursor-not-allowed disabled:opacity-60 ${focusRing}`

export const cardClass = 'rounded-xl border border-gray-700 bg-gray-800 p-6 shadow-lg'
