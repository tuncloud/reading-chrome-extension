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
