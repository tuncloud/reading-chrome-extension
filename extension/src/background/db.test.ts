import { beforeEach, describe, expect, test } from 'vitest'
import { META, record, segment, testDb } from '../../test/helpers'
import { Db, ReadingDb } from './db'

describe('Db', () => {
  let db: Db
  beforeEach(() => {
    db = testDb()
  })

    test('applyRead stores an unsynced segment and creates its article', async () => {
      expect(await record(db, segment('a'))).toBe(true)
      expect(await db.countUnsynced()).toBe(1)
      const [art] = await db.articles([META.canonicalUrl])
      expect(art).toMatchObject({ title: 'Designing Idempotent APIs', totalSeconds: 60, maxProgress: 0.5 })
    })

    test('recording the same segment twice counts it once', async () => {
      await record(db, segment('a'))
      expect(await record(db, segment('a'))).toBe(false)
      const [art] = await db.articles([META.canonicalUrl])
      expect(art?.totalSeconds).toBe(60)
      expect(await db.countUnsynced()).toBe(1)
    })

    test('unsyncedSegments is oldest first and limited', async () => {
      await record(db, segment('late', { startedAt: '2026-10-06T05:00:00.000Z' }))
      await record(db, segment('early', { startedAt: '2026-10-06T01:00:00.000Z' }))
      await record(db, segment('mid', { startedAt: '2026-10-06T03:00:00.000Z' }))
      expect((await db.unsyncedSegments(2)).map((s) => s.id)).toEqual(['early', 'mid'])
    })

    test('markSynced and deleteSegments', async () => {
      await record(db, segment('a'))
      await record(db, segment('b'))
      await record(db, segment('c'))
      await db.markSynced(['a'])
      await db.deleteSegments(['b'])
      expect((await db.unsyncedSegments(10)).map((s) => s.id)).toEqual(['c'])
      expect(await db.countUnsynced()).toBe(1)
    })

    test('articles skips unknown URLs', async () => {
      await record(db, segment('a'))
      expect(await db.articles(['https://nope.example/', META.canonicalUrl])).toHaveLength(1)
    })

    test('recentArticles is latest first and limited', async () => {
      for (const [i, hour] of ['01', '03', '02'].entries()) {
        await record(db, segment(`s${i}`, {
          canonicalUrl: `https://example.com/${hour}`, endedAt: `2026-10-06T${hour}:00:00.000Z`,
        }))
      }
      expect((await db.recentArticles(2)).map((a) => a.canonicalUrl)).toEqual(['https://example.com/03', 'https://example.com/02'])
    })

    test('segmentsSince filters by started_at', async () => {
      await record(db, segment('old', { startedAt: '2026-10-05T00:00:00.000Z' }))
      await record(db, segment('new', { startedAt: '2026-10-06T00:00:00.000Z' }))
      expect((await db.segmentsSince('2026-10-06T00:00:00.000Z')).map((s) => s.id)).toEqual(['new'])
    })

    test('prune keeps unsynced segments', async () => {
      await record(db, segment('old-synced', { startedAt: '2026-08-01T00:00:00.000Z' }))
      await record(db, segment('old-unsynced', { startedAt: '2026-08-02T00:00:00.000Z' }))
      await record(db, segment('new-synced', { startedAt: '2026-10-01T00:00:00.000Z' }))
      await db.markSynced(['old-synced', 'new-synced'])
      expect(await db.pruneSynced('2026-09-06T00:00:00.000Z')).toBe(1)
      expect((await db.segmentsSince('2000-01-01T00:00:00.000Z')).map((s) => s.id).sort()).toEqual(['new-synced', 'old-unsynced'])
    })

    test('clearReading empties articles and segments but keeps kv', async () => {
      await record(db, segment('a'))
      await db.setKv('device_token', 'rt_x')
      await db.clearReading()
      expect(await db.countUnsynced()).toBe(0)
      expect(await db.recentArticles(5)).toEqual([])
      expect(await db.getKv('device_token')).toBe('rt_x')
    })

    test('kv round-trips structured values and deletes keys', async () => {
      expect(await db.getKv('installation')).toBeUndefined()
      const inst = { id: 'b1', workspaceName: 'W', status: 'active', setupError: '', libraryUrl: null }
      await db.setKv('installation', inst)
      await db.setKv('enabled_domains', ['example.com'])
      expect(await db.getKv('installation')).toEqual(inst)
      await db.deleteKv('installation', 'enabled_domains')
      expect(await db.getKv('installation')).toBeUndefined()
      expect(await db.getKv('enabled_domains')).toBeUndefined()
    })
})

test('rows use the snake_case field names of spec §3.4', async () => {
  const raw = new ReadingDb(`test-${crypto.randomUUID()}`)
  const store = new Db(raw)
  await store.applyRead(
    { id: 's1', sessionId: 'x', seq: 0, canonicalUrl: 'https://e.com/a', startedAt: '2026-10-06T03:00:00.000Z',
      endedAt: '2026-10-06T03:01:00.000Z', activeSeconds: 60, maxProgress: 0.5, synced: 0, createdAt: '2026-10-06T03:01:00.000Z' },
    () => ({ canonicalUrl: 'https://e.com/a', source: 'web', title: 'T', author: '', siteName: '', wordCount: 0,
      publishedAt: '', originalUrl: 'https://e.com/a', totalSeconds: 60, maxProgress: 0.5, lastReadAt: '2026-10-06T03:01:00.000Z' }),
  )
  expect(Object.keys((await raw.segments.get('s1'))!).sort()).toEqual([
    'active_seconds', 'canonical_url', 'created_at', 'ended_at', 'id', 'max_progress', 'seq', 'session_id', 'started_at', 'synced',
  ])
  expect(Object.keys((await raw.articles.get('https://e.com/a'))!).sort()).toEqual([
    'author', 'canonical_url', 'last_read_at', 'max_progress', 'original_url', 'published_at', 'site_name', 'source',
    'title', 'total_seconds', 'word_count',
  ])
})
