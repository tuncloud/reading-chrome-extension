import { beforeEach, describe, expect, test, vi, type Mock } from 'vitest'
import { FixedClock, MemoryLog, MemoryStore } from '../../test/fakes'
import { META, segment } from '../../test/store-contract'
import { parseRemoteConfig, type RemoteConfig } from '../shared/config'
import { wireConfig } from '../../test/fixtures'
import { RecordService } from './record'

describe('RecordService', () => {
  let store: MemoryStore
  let syncSoon: Mock<() => void>
  let config: RemoteConfig | null
  let svc: RecordService
  const log = new MemoryLog()

  beforeEach(() => {
    store = new MemoryStore()
    syncSoon = vi.fn<() => void>()
    config = parseRemoteConfig({ ...wireConfig, tracking: { ...wireConfig.tracking, sync_threshold_segments: 2 } })
    svc = new RecordService({ store, clock: new FixedClock(), config: async () => config, syncSoon, log })
  })

  const plain = (id: string) => {
    const { synced: _s, createdAt: _c, ...seg } = segment(id)
    return seg
  }

  test('stores the segment unsynced with created_at and merges the article', async () => {
    expect(await svc.record(META, plain('a'))).toBe(true)
    expect(store.segmentRows.get('a')).toMatchObject({ synced: 0, createdAt: '2026-10-06T03:00:00.000Z' })
    expect(store.articleRows.get(META.canonicalUrl)?.totalSeconds).toBe(60)
  })

  test('C23: a 0-second segment is never stored', async () => {
    expect(await svc.record(META, { ...plain('a'), activeSeconds: 0 })).toBe(false)
    expect(store.segmentRows.size).toBe(0)
  })

  test('a segment whose URL does not match its article is dropped', async () => {
    expect(await svc.record(META, { ...plain('a'), canonicalUrl: 'https://other.example/' })).toBe(false)
    expect(store.segmentRows.size).toBe(0)
  })

  test('asks for a sync once unsynced segments reach sync_threshold_segments', async () => {
    await svc.record(META, plain('a'))
    expect(syncSoon).not.toHaveBeenCalled()
    await svc.record(META, plain('b'))
    expect(syncSoon).toHaveBeenCalledTimes(1)
  })

  test('a duplicate delivery does not trigger a sync or count again', async () => {
    await svc.record(META, plain('a'))
    expect(await svc.record(META, plain('a'))).toBe(false)
    expect(syncSoon).not.toHaveBeenCalled()
  })

  test('without config it still records but never asks to sync', async () => {
    config = null
    await svc.record(META, plain('a'))
    await svc.record(META, plain('b'))
    expect(store.segmentRows.size).toBe(2)
    expect(syncSoon).not.toHaveBeenCalled()
  })
})
