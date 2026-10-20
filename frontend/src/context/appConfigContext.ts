import { createContext } from 'react'
import type { AppConfig } from '../api/types'

export const AppConfigContext = createContext<AppConfig | null>(null)
