// Test doubles for the application ports. MemoryStore passes the same contract as DexieStore.
import type { LocalArticle, StoredSegment } from '../src/domain/reading'
import type { Clock, KvKey, KvSchema, LocalStore, Log } from '../src/application/ports'

export class MemoryStore implements LocalStore {
  readonly articleRows = new Map<string, LocalArticle>()
  readonly segmentRows = new Map<string, StoredSegment>()
  readonly kv = new Map<string, unknown>()

  async applyRead(segment: StoredSegment, merge: (prev: LocalArticle | undefined) => LocalArticle): Promise<boolean> {
    if (this.segmentRows.has(segment.id)) return false
    this.articleRows.set(segment.canonicalUrl, merge(this.articleRows.get(segment.canonicalUrl)))
    this.segmentRows.set(segment.id, { ...segment })
    return true
  }

  async unsyncedSegments(limit: number): Promise<StoredSegment[]> {
    return [...this.segmentRows.values()]
      .filter((s) => s.synced === 0)
      .sort((a, b) => (a.startedAt < b.startedAt ? -1 : a.startedAt > b.startedAt ? 1 : 0))
      .slice(0, limit)
  }

  async countUnsynced(): Promise<number> {
    return [...this.segmentRows.values()].filter((s) => s.synced === 0).length
  }

  async markSynced(ids: readonly string[]): Promise<void> {
    for (const id of ids) {
      const s = this.segmentRows.get(id)
      if (s) s.synced = 1
    }
  }

  async deleteSegments(ids: readonly string[]): Promise<void> {
    for (const id of ids) this.segmentRows.delete(id)
  }

  async articles(urls: readonly string[]): Promise<LocalArticle[]> {
    return urls.flatMap((u) => {
      const a = this.articleRows.get(u)
      return a ? [a] : []
    })
  }

  async recentArticles(limit: number): Promise<LocalArticle[]> {
    return [...this.articleRows.values()].sort((a, b) => (a.lastReadAt < b.lastReadAt ? 1 : -1)).slice(0, limit)
  }

  async segmentsSince(iso: string): Promise<StoredSegment[]> {
    return [...this.segmentRows.values()].filter((s) => s.startedAt >= iso)
  }

  async pruneSynced(beforeIso: string): Promise<number> {
    let n = 0
    for (const [id, s] of this.segmentRows) {
      if (s.synced === 1 && s.startedAt < beforeIso) {
        this.segmentRows.delete(id)
        n++
      }
    }
    return n
  }

  async clearReading(): Promise<void> {
    this.articleRows.clear()
    this.segmentRows.clear()
  }

  async getKv<K extends KvKey>(key: K): Promise<KvSchema[K] | undefined> {
    return this.kv.get(key) as KvSchema[K] | undefined
  }

  async setKv<K extends KvKey>(key: K, value: KvSchema[K]): Promise<void> {
    this.kv.set(key, value)
  }

  async deleteKv(...keys: KvKey[]): Promise<void> {
    for (const k of keys) this.kv.delete(k)
  }
}

export class FixedClock implements Clock {
  constructor(public current: Date = new Date('2026-10-06T03:00:00.000Z'), private readonly zone = 'Asia/Ho_Chi_Minh') {}
  now(): Date {
    return new Date(this.current)
  }
  timeZone(): string {
    return this.zone
  }
  advance(ms: number): void {
    this.current = new Date(this.current.getTime() + ms)
  }
}

export class MemoryLog implements Log {
  readonly lines: string[] = []
  info(message: string): void {
    this.lines.push(`info ${message}`)
  }
  warn(message: string): void {
    this.lines.push(`warn ${message}`)
  }
}

import type { Api, Scheduler } from '../src/application/ports'
import type { ArticleStatus, Installation, Me, NotionPage, PushBody, PushResult, TokenGrant } from '../src/application/types'
import { parseRemoteConfig, type RemoteConfig } from '../src/domain/config'
import { wireConfig } from './fixtures'

export const INSTALLATION: Installation = {
  id: 'b1', workspaceName: "Tuan's Notion", status: 'active', setupError: '', libraryUrl: 'https://www.notion.so/lib',
}

// FakeApi answers like a healthy backend unless a method is overridden.
export class FakeApi implements Api {
  readonly calls: { method: keyof Api; args: unknown[] }[] = []

  constructor(private readonly over: Partial<Api> = {}) {}

  private record(method: keyof Api, args: unknown[]): void {
    this.calls.push({ method, args })
  }

  count(method: keyof Api): number {
    return this.calls.filter((c) => c.method === method).length
  }

  oauthStartUrl(p: { extRedirect: string; codeChallenge: string }): string {
    this.record('oauthStartUrl', [p])
    return this.over.oauthStartUrl?.(p) ?? `https://api.example.com/v1/oauth/notion/start?code_challenge=${p.codeChallenge}`
  }

  async config(): Promise<RemoteConfig> {
    this.record('config', [])
    return this.over.config ? this.over.config() : parseRemoteConfig(wireConfig)
  }

  async exchangeCode(p: { code: string; codeVerifier: string; deviceName: string }): Promise<TokenGrant> {
    this.record('exchangeCode', [p])
    return this.over.exchangeCode
      ? this.over.exchangeCode(p)
      : { deviceToken: 'rt_new', deviceId: 'd1', user: { id: 'u1', name: 'Tuan Do' }, installation: INSTALLATION }
  }

  async me(token: string): Promise<Me> {
    this.record('me', [token])
    return this.over.me
      ? this.over.me(token)
      : { user: { id: 'u1', name: 'Tuan Do' }, device: { id: 'd1', name: 'Mac' }, installation: INSTALLATION }
  }

  async logout(token: string): Promise<void> {
    this.record('logout', [token])
    if (this.over.logout) await this.over.logout(token)
  }

  async deleteAccount(token: string): Promise<void> {
    this.record('deleteAccount', [token])
    if (this.over.deleteAccount) await this.over.deleteAccount(token)
  }

  async push(token: string, body: PushBody): Promise<PushResult> {
    this.record('push', [token, body])
    return this.over.push
      ? this.over.push(token, body)
      : { accepted: body.segments.map((s) => s.id), duplicate: [], retry: [], rejected: [] }
  }

  async articlesStatus(token: string, urls: readonly string[]): Promise<Record<string, ArticleStatus>> {
    this.record('articlesStatus', [token, urls])
    return this.over.articlesStatus ? this.over.articlesStatus(token, urls) : {}
  }

  async setupPages(token: string, query: string): Promise<NotionPage[]> {
    this.record('setupPages', [token, query])
    return this.over.setupPages ? this.over.setupPages(token, query) : []
  }

  async setup(token: string, parentPageId: string): Promise<Installation> {
    this.record('setup', [token, parentPageId])
    return this.over.setup ? this.over.setup(token, parentPageId) : INSTALLATION
  }
}

export class FakeScheduler implements Scheduler {
  readonly alarms = new Map<string, number>()
  async every(name: string, periodMinutes: number): Promise<void> {
    this.alarms.set(name, periodMinutes)
  }
}

import type { AuthFlow, ContentScripts, Permissions, Pkce } from '../src/application/ports'

export class FakeAuthFlow implements AuthFlow {
  readonly launched: string[] = []
  constructor(public respond: (url: string) => Promise<string> = async () => 'https://ext.chromiumapp.org/?code=one-time') {}
  redirectUrl(): string {
    return 'https://ext.chromiumapp.org/'
  }
  launch(url: string): Promise<string> {
    this.launched.push(url)
    return this.respond(url)
  }
}

export class FakePkce implements Pkce {
  async create(): Promise<{ verifier: string; challenge: string }> {
    return { verifier: 'v'.repeat(64), challenge: 'challenge-of-v' }
  }
}

export class FakePermissions implements Permissions {
  readonly granted = new Set<string>()
  readonly events: string[]
  constructor(public allow = true, events: string[] = []) {
    this.events = events
  }
  async request(origins: string[]): Promise<boolean> {
    this.events.push(`request ${origins.join(' ')}`)
    if (this.allow) for (const o of origins) this.granted.add(o)
    return this.allow
  }
  async remove(origins: string[]): Promise<boolean> {
    this.events.push(`remove ${origins.join(' ')}`)
    for (const o of origins) this.granted.delete(o)
    return true
  }
}

export class FakeContentScripts implements ContentScripts {
  readonly scripts = new Map<string, string[]>()
  async register(id: string, matches: string[]): Promise<void> {
    this.scripts.set(id, matches)
  }
  async unregister(ids: string[]): Promise<void> {
    for (const id of ids) this.scripts.delete(id)
  }
  async registeredIds(): Promise<string[]> {
    return [...this.scripts.keys()]
  }
}
