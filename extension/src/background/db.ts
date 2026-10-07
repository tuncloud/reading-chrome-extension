// Db is the LocalStore over IndexedDB (spec §3.4). Rows keep the spec's snake_case
// names; the mapping to domain types lives here only.
import Dexie, { type Table } from 'dexie'
import type { LocalArticle, StoredSegment } from '../shared/reading'
import type { Installation, CachedConfig, User } from '../shared/types'
import type { LocalStore } from '../application/ports'

// KvSchema is the kv table of spec §3.4.
export interface KvSchema {
  device_token: string
  device_id: string
  user: User
  installation: Installation | null
  config: CachedConfig
  enabled_domains: string[]
  denied_domains: string[]
  sync_backoff_until: string
  sync_failures: number
}

export type KvKey = keyof KvSchema

interface ArticleRow {
  canonical_url: string
  source: string
  title: string
  author: string
  site_name: string
  word_count: number
  published_at: string
  original_url: string
  total_seconds: number
  max_progress: number
  last_read_at: string
}

interface SegmentRow {
  id: string
  session_id: string
  seq: number
  canonical_url: string
  started_at: string
  ended_at: string
  active_seconds: number
  max_progress: number
  synced: 0 | 1
  created_at: string
}

interface KvRow {
  key: string
  value: unknown
}

export class ReadingDb extends Dexie {
  declare articles: Table<ArticleRow, string>
  declare segments: Table<SegmentRow, string>
  declare kv: Table<KvRow, string>

  constructor(name = 'reading-tracker') {
    super(name)
    this.version(1).stores({
      articles: 'canonical_url, last_read_at',
      segments: 'id, synced, started_at, [synced+started_at]',
      kv: 'key',
    })
  }
}

function toArticle(r: ArticleRow): LocalArticle {
  return {
    canonicalUrl: r.canonical_url, source: r.source, title: r.title, author: r.author, siteName: r.site_name,
    wordCount: r.word_count, publishedAt: r.published_at, originalUrl: r.original_url,
    totalSeconds: r.total_seconds, maxProgress: r.max_progress, lastReadAt: r.last_read_at,
  }
}

function fromArticle(a: LocalArticle): ArticleRow {
  return {
    canonical_url: a.canonicalUrl, source: a.source, title: a.title, author: a.author, site_name: a.siteName,
    word_count: a.wordCount, published_at: a.publishedAt, original_url: a.originalUrl,
    total_seconds: a.totalSeconds, max_progress: a.maxProgress, last_read_at: a.lastReadAt,
  }
}

function toSegment(r: SegmentRow): StoredSegment {
  return {
    id: r.id, sessionId: r.session_id, seq: r.seq, canonicalUrl: r.canonical_url, startedAt: r.started_at,
    endedAt: r.ended_at, activeSeconds: r.active_seconds, maxProgress: r.max_progress, synced: r.synced, createdAt: r.created_at,
  }
}

function fromSegment(s: StoredSegment): SegmentRow {
  return {
    id: s.id, session_id: s.sessionId, seq: s.seq, canonical_url: s.canonicalUrl, started_at: s.startedAt,
    ended_at: s.endedAt, active_seconds: s.activeSeconds, max_progress: s.maxProgress, synced: s.synced, created_at: s.createdAt,
  }
}

export class Db implements LocalStore {
  constructor(private readonly db: ReadingDb) {}

  applyRead(segment: StoredSegment, merge: (prev: LocalArticle | undefined) => LocalArticle): Promise<boolean> {
    const { db } = this
    return db.transaction('rw', db.articles, db.segments, async () => {
      if (await db.segments.get(segment.id)) return false
      const prev = await db.articles.get(segment.canonicalUrl)
      await db.articles.put(fromArticle(merge(prev ? toArticle(prev) : undefined)))
      await db.segments.put(fromSegment(segment))
      return true
    })
  }

  async unsyncedSegments(limit: number): Promise<StoredSegment[]> {
    const rows = await this.db.segments
      .where('[synced+started_at]')
      .between([0, Dexie.minKey], [0, Dexie.maxKey])
      .limit(limit)
      .toArray()
    return rows.map(toSegment)
  }

  countUnsynced(): Promise<number> {
    return this.db.segments.where('synced').equals(0).count()
  }

  async markSynced(ids: readonly string[]): Promise<void> {
    if (ids.length === 0) return
    await this.db.segments.where('id').anyOf([...ids]).modify({ synced: 1 })
  }

  async deleteSegments(ids: readonly string[]): Promise<void> {
    await this.db.segments.bulkDelete([...ids])
  }

  async articles(urls: readonly string[]): Promise<LocalArticle[]> {
    const rows = await this.db.articles.bulkGet([...urls])
    return rows.flatMap((r) => (r ? [toArticle(r)] : []))
  }

  async recentArticles(limit: number): Promise<LocalArticle[]> {
    const rows = await this.db.articles.orderBy('last_read_at').reverse().limit(limit).toArray()
    return rows.map(toArticle)
  }

  async segmentsSince(iso: string): Promise<StoredSegment[]> {
    const rows = await this.db.segments.where('started_at').aboveOrEqual(iso).toArray()
    return rows.map(toSegment)
  }

  pruneSynced(beforeIso: string): Promise<number> {
    return this.db.segments.where('[synced+started_at]').between([1, Dexie.minKey], [1, beforeIso]).delete()
  }

  async clearReading(): Promise<void> {
    const { db } = this
    await db.transaction('rw', db.articles, db.segments, async () => {
      await db.articles.clear()
      await db.segments.clear()
    })
  }

  async getKv<K extends KvKey>(key: K): Promise<KvSchema[K] | undefined> {
    return (await this.db.kv.get(key))?.value as KvSchema[K] | undefined
  }

  async setKv<K extends KvKey>(key: K, value: KvSchema[K]): Promise<void> {
    await this.db.kv.put({ key, value })
  }

  async deleteKv(...keys: KvKey[]): Promise<void> {
    await this.db.kv.bulkDelete(keys)
  }
}
