// SyncService pushes the local outbox to POST /v1/sync/push (spec §2.8, T28).
import type { ArticleMeta, LocalArticle, Segment, StoredSegment } from '../shared/reading'
import { isApiError } from '../shared/errors'
import type { Log } from '../shared/log'
import { AUTH_KEYS } from './session'
import type { ApiClient } from './api'
import type { Db } from './db'
import type { PushResult, SyncOutcome } from '../shared/types'

export type { SyncOutcome } from '../shared/types'

// PUSH_LIMIT is the backend's per-request segment cap (C25).
export const PUSH_LIMIT = 50
// RETENTION_DAYS is how long synced segments stay local (spec §3.4).
export const RETENTION_DAYS = 30
const MAX_BACKOFF_MINUTES = 30
const MINUTE = 60_000
const DAY = 24 * 60 * MINUTE

export interface SyncDeps {
  db: Db
  api: Pick<ApiClient, 'push'>
  clock: { now(): Date; timeZone(): string }
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

// TOO_LARGE is the one batch-level refusal a smaller batch can cure: the backend's 256 KB body
// limit or its 50-item cap. A 400 is never poison: the backend answers 400 only for request-level
// faults (tz, malformed JSON) and reports a segment's own problems in `rejected`, so a 400 backs
// off like any other failure and keeps every segment.
const TOO_LARGE = 413

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
    const { db, clock } = this.deps
    return db.pruneSynced(new Date(clock.now().getTime() - RETENTION_DAYS * DAY).toISOString())
  }

  private async loop(): Promise<SyncOutcome> {
    const { db, api, clock, log } = this.deps
    const token = await db.getKv('device_token')
    if (!token) return 'disconnected'
    const until = await db.getKv('sync_backoff_until')
    if (until && clock.now().toISOString() < until) return 'backoff'

    for (;;) {
      const segments = await db.unsyncedSegments(PUSH_LIMIT)
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

  // push sends one batch and settles the answer. A batch refused as too large (413) is retried in
  // halves, and a single segment still too large is dropped, so one oversized segment cannot
  // block the outbox. Every other failure propagates and backs off with the data kept.
  private async push(token: string, segments: StoredSegment[]): Promise<Progress> {
    const { db, api, clock, log } = this.deps
    const articles = await db.articles([...new Set(segments.map((s) => s.canonicalUrl))])
    let res: PushResult
    try {
      res = await api.push(token, {
        clientTime: clock.now().toISOString(),
        tz: clock.timeZone(),
        articles: articles.map(meta),
        segments: segments.map(plain),
      })
    } catch (e) {
      if (!isApiError(e, TOO_LARGE)) throw e
      const only = segments[0]!
      if (segments.length === 1) {
        await db.deleteSegments([only.id])
        log.warn(`sync: dropped segment ${only.id} (${e.status} ${e.code})`)
        return { settled: 1, retry: false }
      }
      const mid = Math.ceil(segments.length / 2)
      const first = await this.push(token, segments.slice(0, mid))
      if (first.retry) return first
      const rest = await this.push(token, segments.slice(mid))
      return { settled: first.settled + rest.settled, retry: rest.retry }
    }
    await db.markSynced([...res.accepted, ...res.duplicate])
    if (res.rejected.length > 0) {
      await db.deleteSegments(res.rejected.map((r) => r.id))
      for (const r of res.rejected) log.warn(`sync: server rejected segment ${r.id}: ${r.reason}`)
    }
    return { settled: res.accepted.length + res.duplicate.length + res.rejected.length, retry: res.retry.length > 0 }
  }

  private async fail(e: unknown): Promise<SyncOutcome> {
    const { db, clock, log } = this.deps
    if (isApiError(e, 401)) {
      await db.deleteKv(...AUTH_KEYS, 'sync_failures', 'sync_backoff_until')
      log.warn('sync: device token rejected; disconnected')
      return 'disconnected'
    }
    const failures = ((await db.getKv('sync_failures')) ?? 0) + 1
    const minutes = Math.min(2 ** (failures - 1), MAX_BACKOFF_MINUTES)
    await db.setKv('sync_failures', failures)
    await db.setKv('sync_backoff_until', new Date(clock.now().getTime() + minutes * MINUTE).toISOString())
    log.warn(`sync: push failed (${e instanceof Error ? e.message : String(e)}); retrying in ${minutes} min`)
    return 'failed'
  }

  private async clearBackoff(): Promise<void> {
    await this.deps.db.deleteKv('sync_failures', 'sync_backoff_until')
  }
}
