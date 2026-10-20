import { useContext } from 'react'
import type { AppConfig } from '../api/types'
import { AppConfigContext } from './appConfigContext'

export function useAppConfig(): AppConfig {
  const value = useContext(AppConfigContext)
  if (!value) throw new Error('useAppConfig must be used inside <AppConfigProvider>')
  return value
}
