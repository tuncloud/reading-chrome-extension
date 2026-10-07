// RecordService stores one tracked segment and its article (spec T28: "on segment message
// upsert article + put segment (synced=0)") and asks for a sync past the threshold.
import type { RemoteConfig } from '../shared/config'
import { mergeArticle, type ArticleMeta, type Segment } from '../shared/reading'
import type { Clock, LocalStore, Log } from './ports'

export interface RecordDeps {
  store: LocalStore
  clock: Clock
  config: () => Promise<RemoteConfig | null>
  // syncSoon starts a sync without waiting for it.
  syncSoon: () => void
  log: Log
}

export class RecordService {
  constructor(private readonly deps: RecordDeps) {}

  async record(meta: ArticleMeta, segment: Segment): Promise<boolean> {
    const { store, clock, log } = this.deps
    if (!(segment.activeSeconds >= 1)) {
      log.warn('record: dropped a segment with no active seconds', segment.id)
      return false
    }
    if (segment.canonicalUrl !== meta.canonicalUrl) {
      log.warn('record: segment URL does not match its article', segment.id)
      return false
    }
    const stored = await store.applyRead(
      { ...segment, synced: 0, createdAt: clock.now().toISOString() },
      (prev) => mergeArticle(prev, meta, segment),
    )
    if (!stored) return false
    const config = await this.deps.config()
    if (config && (await store.countUnsynced()) >= config.tracking.syncThresholdSegments) this.deps.syncSoon()
    return true
  }
}
