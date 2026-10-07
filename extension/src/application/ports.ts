// Ports: what the use cases need from the outside world. Adapters implement them.
import type { RemoteConfig } from '../shared/config'
import type { LocalArticle, StoredSegment } from '../shared/reading'
import type { KvKey, KvSchema } from '../background/db'
import type {
  ArticleStatus, Installation, Me, NotionPage, PushBody, PushResult, TokenGrant,
} from '../shared/types'

export interface LocalStore {
  // applyRead stores a new segment and folds it into its article in one transaction.
  // It returns false, changing nothing, when the segment id is already stored.
  applyRead(segment: StoredSegment, merge: (prev: LocalArticle | undefined) => LocalArticle): Promise<boolean>
  // unsyncedSegments returns up to limit unsynced segments, oldest started_at first.
  unsyncedSegments(limit: number): Promise<StoredSegment[]>
  countUnsynced(): Promise<number>
  markSynced(ids: readonly string[]): Promise<void>
  deleteSegments(ids: readonly string[]): Promise<void>
  articles(urls: readonly string[]): Promise<LocalArticle[]>
  // recentArticles returns up to limit articles, latest last_read_at first.
  recentArticles(limit: number): Promise<LocalArticle[]>
  // segmentsSince returns segments with started_at >= iso.
  segmentsSince(iso: string): Promise<StoredSegment[]>
  // pruneSynced deletes synced segments that started before beforeIso and returns how many.
  pruneSynced(beforeIso: string): Promise<number>
  // clearReading deletes every article and segment; kv is kept.
  clearReading(): Promise<void>
  getKv<K extends KvKey>(key: K): Promise<KvSchema[K] | undefined>
  setKv<K extends KvKey>(key: K, value: KvSchema[K]): Promise<void>
  deleteKv(...keys: KvKey[]): Promise<void>
}

// Api is the backend (spec §2). Failures are ApiError (non-2xx) or NetworkError.
export interface Api {
  oauthStartUrl(p: { extRedirect: string; codeChallenge: string }): string
  config(): Promise<RemoteConfig>
  exchangeCode(p: { code: string; codeVerifier: string; deviceName: string }): Promise<TokenGrant>
  me(token: string): Promise<Me>
  logout(token: string): Promise<void>
  deleteAccount(token: string): Promise<void>
  push(token: string, body: PushBody): Promise<PushResult>
  articlesStatus(token: string, urls: readonly string[]): Promise<Record<string, ArticleStatus>>
  setupPages(token: string, query: string): Promise<NotionPage[]>
  setup(token: string, parentPageId: string): Promise<Installation>
}

export interface Clock {
  now(): Date
  // timeZone is the IANA zone the user reads in (sent as "tz" on push).
  timeZone(): string
}

// AuthFlow is chrome.identity's web auth flow.
export interface AuthFlow {
  redirectUrl(): string
  // launch resolves with the final redirect URL; it rejects when the user closes the window.
  launch(url: string): Promise<string>
}

export interface Pkce {
  create(): Promise<{ verifier: string; challenge: string }>
}

export interface Permissions {
  request(origins: string[]): Promise<boolean>
  remove(origins: string[]): Promise<boolean>
}

export interface ContentScripts {
  register(id: string, matches: string[]): Promise<void>
  unregister(ids: string[]): Promise<void>
  registeredIds(): Promise<string[]>
}

export interface Scheduler {
  // every (re)creates a repeating alarm.
  every(name: string, periodMinutes: number): Promise<void>
}

export type { Log } from '../shared/log'
export type { KvSchema, KvKey } from '../background/db'
