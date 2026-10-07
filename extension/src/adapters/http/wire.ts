// The snake_case JSON of spec §2 and its mapping to application types.
import type { ArticleStatus, Installation, Me, PushBody, PushResult, TokenGrant } from '../../application/types'

export interface WireInstallation {
  id: string
  workspace_name: string
  status: string
  setup_error?: string
  library_url: string | null
}

export interface WireUser {
  id: string
  name: string
}

export interface WireToken {
  device_token: string
  device_id: string
  user: WireUser
  installation: WireInstallation | null
}

export interface WireMe {
  user: WireUser
  device: { id: string; name: string }
  installation: WireInstallation | null
}

export interface WirePushResult {
  accepted: string[] | null
  duplicate: string[] | null
  retry: string[] | null
  rejected: { id: string; reason: string }[] | null
}

export interface WireArticleStatus {
  total_seconds: number
  max_progress: number
  sessions: number
  completed: boolean
  last_read_at: string
  notion_url: string | null
}

export function installation(w: WireInstallation | null): Installation | null {
  if (!w) return null
  return {
    id: w.id, workspaceName: w.workspace_name, status: w.status, setupError: w.setup_error ?? '', libraryUrl: w.library_url ?? null,
  }
}

export function tokenGrant(w: WireToken): TokenGrant {
  return { deviceToken: w.device_token, deviceId: w.device_id, user: { id: w.user.id, name: w.user.name }, installation: installation(w.installation) }
}

export function me(w: WireMe): Me {
  return {
    user: { id: w.user.id, name: w.user.name }, device: { id: w.device.id, name: w.device.name }, installation: installation(w.installation),
  }
}

export function pushBody(b: PushBody): unknown {
  return {
    client_time: b.clientTime,
    tz: b.tz,
    articles: b.articles.map((a) => ({
      canonical_url: a.canonicalUrl, source: a.source, title: a.title, author: a.author, site_name: a.siteName,
      word_count: a.wordCount, published_at: a.publishedAt, original_url: a.originalUrl,
    })),
    segments: b.segments.map((s) => ({
      id: s.id, session_id: s.sessionId, seq: s.seq, canonical_url: s.canonicalUrl, started_at: s.startedAt,
      ended_at: s.endedAt, active_seconds: s.activeSeconds, max_progress: s.maxProgress,
    })),
  }
}

export function pushResult(w: WirePushResult): PushResult {
  return { accepted: w.accepted ?? [], duplicate: w.duplicate ?? [], retry: w.retry ?? [], rejected: w.rejected ?? [] }
}

export function articleStatus(w: WireArticleStatus): ArticleStatus {
  return {
    totalSeconds: w.total_seconds, maxProgress: w.max_progress, sessions: w.sessions, completed: w.completed,
    lastReadAt: w.last_read_at, notionUrl: w.notion_url ?? null,
  }
}
