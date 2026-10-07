import { beforeEach, describe, expect, test } from 'vitest'
import { FakeApi, FixedClock, MemoryLog, MemoryStore } from '../../test/fakes'
import { record, segment } from '../../test/store-contract'
import { ApiError, NetworkError } from './errors'
import type { PushBody, PushResult } from './types'
import { SyncService } from './sync'

const MIN = 60_000

async function seed(store: MemoryStore, n: number, urls = ['https://e.com/a', 'https://e.com/b']): Promise<string[]> {
  const ids: string[] = []
  for (let i = 0; i < n; i++) {
    const id = `s${String(i).padStart(3, '0')}`
    ids.push(id)
    const startedAt = new Date(Date.UTC(2026, 9, 6, 0, 0, i)).toISOString()
    await record(store, segment(id, { canonicalUrl: urls[i % urls.length]!, startedAt }))
  }
  return ids
}

describe('SyncService.run', () => {
  let store: MemoryStore
  let clock: FixedClock
  let log: MemoryLog

  beforeEach(async () => {
    store = new MemoryStore()
    clock = new FixedClock()
    log = new MemoryLog()
    await store.setKv('device_token', 'rt_x')
  })

  const service = (api: FakeApi) => new SyncService({ store, api, clock, log })

  test('without a device token it does nothing', async () => {
    await store.deleteKv('device_token')
    await seed(store, 3)
    const api = new FakeApi()
    expect(await service(api).run()).toBe('disconnected')
    expect(api.count('push')).toBe(0)
  })

  test('C25: batches of ≤ 50, oldest first, each with only its own articles', async () => {
    const urls = Array.from({ length: 60 }, (_, i) => `https://e.com/${i}`)
    const ids = await seed(store, 120, urls)
    const api = new FakeApi()
    expect(await service(api).run()).toBe('synced')
    const bodies = api.calls.filter((c) => c.method === 'push').map((c) => c.args[1] as PushBody)
    expect(bodies.map((b) => b.segments.length)).toEqual([50, 50, 20])
    expect(bodies.flatMap((b) => b.segments.map((s) => s.id))).toEqual(ids)
    for (const b of bodies) {
      expect(b.articles.map((a) => a.canonicalUrl).sort()).toEqual([...new Set(b.segments.map((s) => s.canonicalUrl))].sort())
    }
    expect(await store.countUnsynced()).toBe(0)
  })

  test('the envelope carries client_time and the reader time zone; articles carry metadata only', async () => {
    await seed(store, 1)
    const api = new FakeApi()
    await service(api).run()
    const body = api.calls[0]?.args[1] as PushBody
    expect(body.clientTime).toBe('2026-10-06T03:00:00.000Z')
    expect(body.tz).toBe('Asia/Ho_Chi_Minh')
    expect(Object.keys(body.articles[0]!).sort()).toEqual(
      ['author', 'canonicalUrl', 'originalUrl', 'publishedAt', 'siteName', 'source', 'title', 'wordCount'],
    )
    expect(Object.keys(body.segments[0]!)).not.toContain('synced')
  })

  test('§2.8: accepted and duplicate are synced, rejected dropped with a log line, retry kept', async () => {
    await seed(store, 4)
    const api = new FakeApi({
      push: async () => ({ accepted: ['s000'], duplicate: ['s001'], retry: ['s002'], rejected: [{ id: 's003', reason: 'started_at_out_of_range' }] }),
    })
    expect(await service(api).run()).toBe('partial')
    expect((await store.unsyncedSegments(10)).map((s) => s.id)).toEqual(['s002'])
    expect(store.segmentRows.has('s003')).toBe(false)
    expect(log.lines.some((l) => l.includes('s003') && l.includes('started_at_out_of_range'))).toBe(true)
    expect(api.count('push')).toBe(1)
  })

  test('T28: 401 clears the device token and keeps the segments', async () => {
    await seed(store, 2)
    await store.setKv('device_id', 'd1')
    await store.setKv('installation', null)
    const api = new FakeApi({ push: async () => { throw new ApiError(401, 'unauthorized', 'revoked') } })
    expect(await service(api).run()).toBe('disconnected')
    expect(await store.getKv('device_token')).toBeUndefined()
    expect(await store.getKv('device_id')).toBeUndefined()
    expect(await store.getKv('installation')).toBeUndefined()
    expect(await store.countUnsynced()).toBe(2)
  })

  test('T28: failures back off 1, 2, 4 … minutes, capped at 30, and success resets', async () => {
    await seed(store, 1)
    let online = false
    const api = new FakeApi({
      push: async (_t, body) => {
        if (!online) throw new NetworkError('Failed to fetch')
        return { accepted: body.segments.map((s) => s.id), duplicate: [], retry: [], rejected: [] }
      },
    })
    const svc = service(api)
    const waits: number[] = []
    for (let i = 0; i < 7; i++) {
      expect(await svc.run()).toBe('failed')
      const until = Date.parse((await store.getKv('sync_backoff_until'))!)
      waits.push((until - clock.now().getTime()) / MIN)
      expect(await svc.run()).toBe('backoff')
      clock.current = new Date(until)
    }
    expect(waits).toEqual([1, 2, 4, 8, 16, 30, 30])
    online = true
    expect(await svc.run()).toBe('synced')
    expect(await store.getKv('sync_failures')).toBeUndefined()
    expect(await store.getKv('sync_backoff_until')).toBeUndefined()
  })

  test('503 capacity_exceeded backs off too', async () => {
    await seed(store, 1)
    const api = new FakeApi({ push: async () => { throw new ApiError(503, 'capacity_exceeded', 'busy') } })
    expect(await service(api).run()).toBe('failed')
    expect(await store.getKv('sync_failures')).toBe(1)
  })

  test('Review focus 4: concurrent runs share one push', async () => {
    await seed(store, 3)
    let release!: () => void
    const gate = new Promise<void>((r) => { release = r })
    const api = new FakeApi({
      push: async (_t, body) => {
        await gate
        return { accepted: body.segments.map((s) => s.id), duplicate: [], retry: [], rejected: [] }
      },
    })
    const svc = service(api)
    const a = svc.run()
    const b = svc.run()
    release()
    expect(await Promise.all([a, b])).toEqual(['synced', 'synced'])
    expect(api.count('push')).toBe(1)
    expect(await svc.run()).toBe('synced')
    expect(api.count('push')).toBe(1)
  })

  test('T28: offline for an hour, then online → every segment is synced exactly once', async () => {
    const ids = await seed(store, 75)
    const committed = new Map<string, number>()
    let online = false
    const api = new FakeApi({
      push: async (_t, body) => {
        if (!online) throw new NetworkError('offline')
        const res: PushResult = { accepted: [], duplicate: [], retry: [], rejected: [] }
        for (const s of body.segments) {
          if (committed.has(s.id)) res.duplicate.push(s.id)
          else res.accepted.push(s.id)
          committed.set(s.id, (committed.get(s.id) ?? 0) + 1)
        }
        return res
      },
    })
    const svc = service(api)
    for (let t = 0; t < 60; t += 2) {
      await svc.run()
      clock.advance(2 * MIN)
    }
    online = true
    clock.advance(60 * MIN)
    expect(await svc.run()).toBe('synced')
    expect([...committed.keys()].sort()).toEqual(ids)
    expect([...committed.values()].every((n) => n === 1)).toBe(true)
  })

  test('T28: a batch retried after a timeout comes back as duplicate only', async () => {
    const ids = await seed(store, 5)
    const committed = new Set<string>()
    let timeout = true
    const api = new FakeApi({
      push: async (_t, body) => {
        const res: PushResult = { accepted: [], duplicate: [], retry: [], rejected: [] }
        for (const s of body.segments) {
          if (committed.has(s.id)) res.duplicate.push(s.id)
          else { committed.add(s.id); res.accepted.push(s.id) }
        }
        if (timeout) { timeout = false; throw new NetworkError('timeout after commit') }
        return res
      },
    })
    const svc = service(api)
    expect(await svc.run()).toBe('failed')
    clock.advance(MIN)
    expect(await svc.run()).toBe('synced')
    const second = api.calls[1]?.args[1] as PushBody
    expect(second.segments.map((s) => s.id)).toEqual(ids)
    expect(await store.countUnsynced()).toBe(0)
  })
})

describe('SyncService.prune', () => {
  test('§3.4: deletes synced segments older than 30 days only', async () => {
    const store = new MemoryStore()
    const clock = new FixedClock(new Date('2026-10-06T00:00:00.000Z'))
    await record(store, segment('old', { startedAt: '2026-09-05T00:00:00.000Z' }))
    await record(store, segment('recent', { startedAt: '2026-09-07T00:00:00.000Z' }))
    await record(store, segment('old-unsynced', { startedAt: '2026-08-01T00:00:00.000Z' }))
    await store.markSynced(['old', 'recent'])
    const svc = new SyncService({ store, api: new FakeApi(), clock, log: new MemoryLog() })
    expect(await svc.prune()).toBe(1)
    expect([...store.segmentRows.keys()].sort()).toEqual(['old-unsynced', 'recent'])
  })
})
