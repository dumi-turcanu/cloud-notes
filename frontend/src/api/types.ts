export interface User {
  id: string
  email: string
  display_name: string | null
  created_at: string
}

export type SignupMode = 'disabled' | 'invite' | 'open'

export type AuthProviderKind = 'local' | 'oidc' | 'identity-platform'

/** Firebase web configuration for GCP Identity Platform. The API key is public by design. */
export interface IdentityPlatformConfig {
  api_key: string
  auth_domain: string
  project_id: string
  tenant_id?: string
  sign_in_methods: Array<'password' | 'google'>
}

export interface AuthConfig {
  provider: AuthProviderKind
  /** Label of the single sign-on button (oidc). */
  display_name?: string
  identity_platform?: IdentityPlatformConfig
}

export interface AppConfig {
  signup_mode: SignupMode
  features: { public_sharing: boolean }
  /** Absent when talking to an older API: treat as `local`. */
  auth?: AuthConfig
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
