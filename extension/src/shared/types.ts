// What the use cases exchange with the backend (spec §2), in camelCase.
import type { RemoteConfig } from './config'
import type { ArticleMeta, Segment } from './reading'

export type InstallationStatus = 'active' | 'needs_setup' | 'revoked'

export interface Installation {
  id: string
  workspaceName: string
  status: InstallationStatus | (string & {})
  setupError: string
  libraryUrl: string | null
}

export interface User {
  id: string
  name: string
}

export interface Me {
  user: User
  device: { id: string; name: string }
  installation: Installation | null
}

export interface TokenGrant {
  deviceToken: string
  deviceId: string
  user: User
  installation: Installation | null
}

export interface PushBody {
  clientTime: string
  tz: string
  articles: ArticleMeta[]
  segments: Segment[]
}

export interface PushResult {
  accepted: string[]
  duplicate: string[]
  retry: string[]
  rejected: { id: string; reason: string }[]
}

export interface ArticleStatus {
  totalSeconds: number
  maxProgress: number
  sessions: number
  completed: boolean
  lastReadAt: string
  notionUrl: string | null
}

export interface NotionPage {
  id: string
  title: string
  url: string
}

export interface CachedConfig {
  fetchedAt: string
  value: RemoteConfig
}

export type SyncOutcome = 'disconnected' | 'backoff' | 'synced' | 'partial' | 'failed'

export type ConnectResult = { ok: true; installation: Installation | null } | { ok: false; reason: string }

export type SessionState =
  | { kind: 'disconnected' }
  | { kind: 'connected'; user: User | null; installation: Installation | null }
