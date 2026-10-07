import { beforeEach, describe, expect, test } from 'vitest'
import { FakeApi, FakeScheduler, FixedClock, MemoryLog, MemoryStore } from '../../test/fakes'
import { wireConfig } from '../../test/fixtures'
import { parseRemoteConfig } from '../shared/config'
import { ConfigService } from './config-service'
import { NetworkError } from '../shared/errors'

const HOUR = 3_600_000

describe('ConfigService', () => {
  let store: MemoryStore
  let clock: FixedClock
  let scheduler: FakeScheduler

  beforeEach(() => {
    store = new MemoryStore()
    clock = new FixedClock()
    scheduler = new FakeScheduler()
  })

  const service = (api: FakeApi) => new ConfigService({ store, api, clock, scheduler, log: new MemoryLog() })
  const cached = (fetchedAt: string, interval = 2) => store.setKv('config', {
    fetchedAt,
    value: parseRemoteConfig({ ...wireConfig, tracking: { ...wireConfig.tracking, sync_interval_minutes: interval } }),
  })

  test('current() answers from the cache without a request', async () => {
    await cached('2026-10-06T02:00:00.000Z')
    const api = new FakeApi()
    expect((await service(api).current())?.tracking.idleSeconds).toBe(60)
    expect(api.count('config')).toBe(0)
  })

  test('current() fetches when nothing is cached, stores it and schedules sync every sync_interval_minutes', async () => {
    const api = new FakeApi()
    const cfg = await service(api).current()
    expect(cfg?.freediumHosts).toEqual(['freedium-mirror.cfd', 'freedium.cfd'])
    expect(await store.getKv('config')).toEqual({ fetchedAt: '2026-10-06T03:00:00.000Z', value: cfg })
    expect(scheduler.alarms.get('sync')).toBe(2)
  })

  test('a failed refresh keeps the cached config, or gives null when there is none', async () => {
    const api = new FakeApi({ config: async () => { throw new NetworkError('offline') } })
    expect(await service(api).refresh()).toBeNull()
    await cached('2026-10-01T00:00:00.000Z')
    expect((await service(api).refresh())?.tracking.segmentSeconds).toBe(60)
    expect((await store.getKv('config'))?.fetchedAt).toBe('2026-10-01T00:00:00.000Z')
  })

  test('T28: refreshIfStale refetches only after a day', async () => {
    await cached(new Date(clock.now().getTime() - 23 * HOUR).toISOString())
    const api = new FakeApi()
    await service(api).refreshIfStale()
    expect(api.count('config')).toBe(0)
    clock.advance(2 * HOUR)
    await service(api).refreshIfStale()
    expect(api.count('config')).toBe(1)
  })

  test('ensureSchedule follows the cached interval and does nothing without config', async () => {
    const api = new FakeApi({ config: async () => { throw new NetworkError('offline') } })
    await service(api).ensureSchedule()
    expect(scheduler.alarms.size).toBe(0)
    await cached('2026-10-06T02:00:00.000Z', 5)
    await service(api).ensureSchedule()
    expect(scheduler.alarms.get('sync')).toBe(5)
  })
})
