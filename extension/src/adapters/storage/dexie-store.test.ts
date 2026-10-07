import 'fake-indexeddb/auto'
import { expect, test } from 'vitest'
import { storeContract } from '../../../test/store-contract'
import { DexieStore, ReadingDb } from './dexie-store'

storeContract('DexieStore', async () => new DexieStore(new ReadingDb(`test-${crypto.randomUUID()}`)))

test('rows use the snake_case field names of spec §3.4', async () => {
  const db = new ReadingDb(`test-${crypto.randomUUID()}`)
  const store = new DexieStore(db)
  await store.applyRead(
    { id: 's1', sessionId: 'x', seq: 0, canonicalUrl: 'https://e.com/a', startedAt: '2026-10-06T03:00:00.000Z',
      endedAt: '2026-10-06T03:01:00.000Z', activeSeconds: 60, maxProgress: 0.5, synced: 0, createdAt: '2026-10-06T03:01:00.000Z' },
    () => ({ canonicalUrl: 'https://e.com/a', source: 'web', title: 'T', author: '', siteName: '', wordCount: 0,
      publishedAt: '', originalUrl: 'https://e.com/a', totalSeconds: 60, maxProgress: 0.5, lastReadAt: '2026-10-06T03:01:00.000Z' }),
  )
  expect(Object.keys((await db.segments.get('s1'))!).sort()).toEqual([
    'active_seconds', 'canonical_url', 'created_at', 'ended_at', 'id', 'max_progress', 'seq', 'session_id', 'started_at', 'synced',
  ])
  expect(Object.keys((await db.articles.get('https://e.com/a'))!).sort()).toEqual([
    'author', 'canonical_url', 'last_read_at', 'max_progress', 'original_url', 'published_at', 'site_name', 'source',
    'title', 'total_seconds', 'word_count',
  ])
})
