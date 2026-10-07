// SyncService pushes the local outbox to POST /v1/sync/push (spec §2.8, T28).
import type { ArticleMeta, LocalArticle, Segment, StoredSegment } from '../domain/reading'
import { isApiError } from './errors'
import { AUTH_KEYS } from './session'
import type { Api, Clock, LocalStore, Log } from './ports'
import type { PushResult } from './types'

export type SyncOutcome = 'disconnected' | 'backoff' | 'synced' | 'partial' | 'failed'

// PUSH_LIMIT is the backend's per-request segment cap (C25).
export const PUSH_LIMIT = 50
// RETENTION_DAYS is how long synced segments stay local (spec §3.4).
export const RETENTION_DAYS = 30
const MAX_BACKOFF_MINUTES = 30
const MINUTE = 60_000
const DAY = 24 * 60 * MINUTE

export interface SyncDeps {
  store: LocalStore
  api: Api
  clock: Clock
  log: Log
}

function meta(a: LocalArticle): ArticleMeta {
  return {
    canonicalUrl: a.canonicalUrl, source: a.source, title: a.title, author: a.author, siteName: a.siteName,
    wordCount: a.wordCount, publishedAt: a.publishedAt, originalUrl: a.originalUrl,
  }
}

function plain(s: StoredSegment): Segment {
  return {
    id: s.id, sessionId: s.sessionId, seq: s.seq, canonicalUrl: s.canonicalUrl, startedAt: s.startedAt,
    endedAt: s.endedAt, activeSeconds: s.activeSeconds, maxProgress: s.maxProgress,
  }
}

// POISON are the statuses that mean "this batch itself is unacceptable", not "try again later".
const POISON = [400, 413]

interface Progress {
  settled: number
  retry: boolean
}

export class SyncService {
  private inflight: Promise<SyncOutcome> | null = null

  constructor(private readonly deps: SyncDeps) {}

  // run pushes until the outbox is empty. Concurrent callers share one run.
  run(): Promise<SyncOutcome> {
    if (!this.inflight) {
      this.inflight = this.loop().finally(() => {
        this.inflight = null
      })
    }
    return this.inflight
  }

  // prune deletes synced segments that started more than RETENTION_DAYS ago.
  prune(): Promise<number> {
    const { store, clock } = this.deps
    return store.pruneSynced(new Date(clock.now().getTime() - RETENTION_DAYS * DAY).toISOString())
  }

  private async loop(): Promise<SyncOutcome> {
    const { store, api, clock, log } = this.deps
    const token = await store.getKv('device_token')
    if (!token) return 'disconnected'
    const until = await store.getKv('sync_backoff_until')
    if (until && clock.now().toISOString() < until) return 'backoff'

    for (;;) {
      const segments = await store.unsyncedSegments(PUSH_LIMIT)
      if (segments.length === 0) break
      let progress: Progress
      try {
        progress = await this.push(token, segments)
      } catch (e) {
        return this.fail(e)
      }
      if (progress.retry) {
        await this.clearBackoff()
        return 'partial'
      }
      // The server settled nothing: stop rather than resend the same batch in a loop.
      if (progress.settled === 0) break
    }
    await this.clearBackoff()
    return 'synced'
  }

  // push sends one batch and settles the answer. A batch the server refuses as a whole (413/400)
  // is poison: it is retried in halves, and a single segment that is still refused is dropped,
  // so one bad segment cannot block the outbox. Other failures propagate.
  private async push(token: string, segments: StoredSegment[]): Promise<Progress> {
    const { store, api, clock, log } = this.deps
    const articles = await store.articles([...new Set(segments.map((s) => s.canonicalUrl))])
    let res: PushResult
    try {
      res = await api.push(token, {
        clientTime: clock.now().toISOString(),
        tz: clock.timeZone(),
        articles: articles.map(meta),
        segments: segments.map(plain),
      })
    } catch (e) {
      if (!isApiError(e) || !POISON.includes(e.status)) throw e
      const only = segments[0]!
      if (segments.length === 1) {
        await store.deleteSegments([only.id])
        log.warn(`sync: dropped segment ${only.id} (${e.status} ${e.code})`)
        return { settled: 1, retry: false }
      }
      const mid = Math.ceil(segments.length / 2)
      const first = await this.push(token, segments.slice(0, mid))
      if (first.retry) return first
      const rest = await this.push(token, segments.slice(mid))
      return { settled: first.settled + rest.settled, retry: rest.retry }
    }
    await store.markSynced([...res.accepted, ...res.duplicate])
    if (res.rejected.length > 0) {
      await store.deleteSegments(res.rejected.map((r) => r.id))
      for (const r of res.rejected) log.warn(`sync: server rejected segment ${r.id}: ${r.reason}`)
    }
    return { settled: res.accepted.length + res.duplicate.length + res.rejected.length, retry: res.retry.length > 0 }
  }

  private async fail(e: unknown): Promise<SyncOutcome> {
    const { store, clock, log } = this.deps
    if (isApiError(e, 401)) {
      await store.deleteKv(...AUTH_KEYS, 'sync_failures', 'sync_backoff_until')
      log.warn('sync: device token rejected; disconnected')
      return 'disconnected'
    }
    const failures = ((await store.getKv('sync_failures')) ?? 0) + 1
    const minutes = Math.min(2 ** (failures - 1), MAX_BACKOFF_MINUTES)
    await store.setKv('sync_failures', failures)
    await store.setKv('sync_backoff_until', new Date(clock.now().getTime() + minutes * MINUTE).toISOString())
    log.warn(`sync: push failed (${e instanceof Error ? e.message : String(e)}); retrying in ${minutes} min`)
    return 'failed'
  }

  private async clearBackoff(): Promise<void> {
    await this.deps.store.deleteKv('sync_failures', 'sync_backoff_until')
  }
}
