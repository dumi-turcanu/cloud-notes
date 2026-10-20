import { apiFetch } from './client'
import type { AppConfig } from './types'

export const getConfig = (signal?: AbortSignal) => apiFetch<AppConfig>('/config', { signal })
