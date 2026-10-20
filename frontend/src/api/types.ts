export interface User {
  id: string
  email: string
  display_name: string | null
  created_at: string
}

export type SignupMode = 'disabled' | 'invite' | 'open'

export interface AppConfig {
  signup_mode: SignupMode
  features: { public_sharing: boolean }
  version: string
}

export interface NoteContent {
  body?: string
  [key: string]: unknown
}

export interface Note {
  id: string
  user_id: string
  title: string
  content: NoteContent
  is_public: boolean
  share_uuid: string
  created_at: string
  updated_at: string
  deleted_at: string | null
}

export interface PublishedNote extends Note {
  share_url: string
}

export interface SharedNote {
  title: string
  content: NoteContent
  updated_at: string
}

export interface NoteInput {
  title: string
  content: NoteContent
}

export interface LoginInput {
  email: string
  password: string
}

export interface RegisterInput extends LoginInput {
  display_name?: string
  invite_code?: string
}
