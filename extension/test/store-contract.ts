// storeContract is the behaviour every LocalStore must have (spec §3.4).
import { beforeEach, describe, expect, test } from 'vitest'
import { mergeArticle, type ArticleMeta, type Segment, type StoredSegment } from '../src/domain/reading'
import type { LocalStore } from '../src/application/ports'

export const META: ArticleMeta = {
  canonicalUrl: 'https://medium.com/p/8f3c2a1b9d4e', source: 'medium', title: 'Designing Idempotent APIs',
  author: 'Jane Doe', siteName: 'Better Programming', wordCount: 1820, publishedAt: '2026-09-30',
  originalUrl: 'https://freedium-mirror.cfd/https://medium.com/x-8f3c2a1b9d4e',
}

export function segment(id: string, over: Partial<StoredSegment> = {}): StoredSegment {
  return {
    id, sessionId: 'sess', seq: 0, canonicalUrl: META.canonicalUrl,
    startedAt: '2026-10-06T03:00:00.000Z', endedAt: '2026-10-06T03:01:00.000Z', activeSeconds: 60, maxProgress: 0.5,
    synced: 0, createdAt: '2026-10-06T03:01:00.000Z', ...over,
  }
}

export async function record(store: LocalStore, seg: StoredSegment, meta: ArticleMeta = META): Promise<boolean> {
  const plain: Segment = { ...seg }
  return store.applyRead(seg, (prev) => mergeArticle(prev, { ...meta, canonicalUrl: seg.canonicalUrl }, plain))
}

export function storeContract(name: string, make: () => Promise<LocalStore>): void {
  describe(`${name} (LocalStore contract)`, () => {
    let store: LocalStore
    beforeEach(async () => {
      store = await make()
    })

    test('applyRead stores an unsynced segment and creates its article', async () => {
      expect(await record(store, segment('a'))).toBe(true)
      expect(await store.countUnsynced()).toBe(1)
      const [art] = await store.articles([META.canonicalUrl])
      expect(art).toMatchObject({ title: 'Designing Idempotent APIs', totalSeconds: 60, maxProgress: 0.5 })
    })

    test('recording the same segment twice counts it once', async () => {
      await record(store, segment('a'))
      expect(await record(store, segment('a'))).toBe(false)
      const [art] = await store.articles([META.canonicalUrl])
      expect(art?.totalSeconds).toBe(60)
      expect(await store.countUnsynced()).toBe(1)
    })

    test('unsyncedSegments is oldest first and limited', async () => {
      await record(store, segment('late', { startedAt: '2026-10-06T05:00:00.000Z' }))
      await record(store, segment('early', { startedAt: '2026-10-06T01:00:00.000Z' }))
      await record(store, segment('mid', { startedAt: '2026-10-06T03:00:00.000Z' }))
      expect((await store.unsyncedSegments(2)).map((s) => s.id)).toEqual(['early', 'mid'])
    })

    test('markSynced and deleteSegments', async () => {
      await record(store, segment('a'))
      await record(store, segment('b'))
      await record(store, segment('c'))
      await store.markSynced(['a'])
      await store.deleteSegments(['b'])
      expect((await store.unsyncedSegments(10)).map((s) => s.id)).toEqual(['c'])
      expect(await store.countUnsynced()).toBe(1)
    })

    test('articles skips unknown URLs', async () => {
      await record(store, segment('a'))
      expect(await store.articles(['https://nope.example/', META.canonicalUrl])).toHaveLength(1)
    })

    test('recentArticles is latest first and limited', async () => {
      for (const [i, hour] of ['01', '03', '02'].entries()) {
        await record(store, segment(`s${i}`, {
          canonicalUrl: `https://example.com/${hour}`, endedAt: `2026-10-06T${hour}:00:00.000Z`,
        }))
      }
      expect((await store.recentArticles(2)).map((a) => a.canonicalUrl)).toEqual(['https://example.com/03', 'https://example.com/02'])
    })

    test('segmentsSince filters by started_at', async () => {
      await record(store, segment('old', { startedAt: '2026-10-05T00:00:00.000Z' }))
      await record(store, segment('new', { startedAt: '2026-10-06T00:00:00.000Z' }))
      expect((await store.segmentsSince('2026-10-06T00:00:00.000Z')).map((s) => s.id)).toEqual(['new'])
    })

    test('prune keeps unsynced segments', async () => {
      await record(store, segment('old-synced', { startedAt: '2026-08-01T00:00:00.000Z' }))
      await record(store, segment('old-unsynced', { startedAt: '2026-08-02T00:00:00.000Z' }))
      await record(store, segment('new-synced', { startedAt: '2026-10-01T00:00:00.000Z' }))
      await store.markSynced(['old-synced', 'new-synced'])
      expect(await store.pruneSynced('2026-09-06T00:00:00.000Z')).toBe(1)
      expect((await store.segmentsSince('2000-01-01T00:00:00.000Z')).map((s) => s.id).sort()).toEqual(['new-synced', 'old-unsynced'])
    })

    test('clearReading empties articles and segments but keeps kv', async () => {
      await record(store, segment('a'))
      await store.setKv('device_token', 'rt_x')
      await store.clearReading()
      expect(await store.countUnsynced()).toBe(0)
      expect(await store.recentArticles(5)).toEqual([])
      expect(await store.getKv('device_token')).toBe('rt_x')
    })

    test('kv round-trips structured values and deletes keys', async () => {
      expect(await store.getKv('installation')).toBeUndefined()
      const inst = { id: 'b1', workspaceName: 'W', status: 'active', setupError: '', libraryUrl: null }
      await store.setKv('installation', inst)
      await store.setKv('enabled_domains', ['example.com'])
      expect(await store.getKv('installation')).toEqual(inst)
      await store.deleteKv('installation', 'enabled_domains')
      expect(await store.getKv('installation')).toBeUndefined()
      expect(await store.getKv('enabled_domains')).toBeUndefined()
    })
  })
}
