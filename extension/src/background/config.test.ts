import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { apiStub, captureLog, clock as makeClock, testDb } from '../../test/helpers'
import { installChrome, type ChromeMock } from '../../test/chrome'
import { wireConfig } from '../../test/fixtures'
import { parseRemoteConfig } from '../shared/config'
import { ConfigService } from './config'
import { NetworkError } from '../shared/errors'
import type { Db } from './db'

const HOUR = 3_600_000

describe('ConfigService', () => {
  let store: Db
  let clock: ReturnType<typeof makeClock>
  let ch: ChromeMock

  beforeEach(() => {
    store = testDb()
    clock = makeClock()
    ch = installChrome()
  })

  afterEach(() => vi.unstubAllGlobals())

  const service = (api: ReturnType<typeof apiStub>) => new ConfigService({ db: store, api, now: clock.now, log: captureLog() })
  const cached = (fetchedAt: string, interval = 2) => store.setKv('config', {
    fetchedAt,
    value: parseRemoteConfig({ ...wireConfig, tracking: { ...wireConfig.tracking, sync_interval_minutes: interval } }),
  })

  test('current() answers from the cache without a request', async () => {
    await cached('2026-10-06T02:00:00.000Z')
    const api = apiStub()
    expect((await service(api).current())?.tracking.idleSeconds).toBe(60)
    expect(api.count('config')).toBe(0)
  })

  test('current() fetches when nothing is cached, stores it and schedules sync every sync_interval_minutes', async () => {
    const api = apiStub()
    const cfg = await service(api).current()
    expect(cfg?.freediumHosts).toEqual(['freedium-mirror.cfd', 'freedium.cfd'])
    expect(await store.getKv('config')).toEqual({ fetchedAt: '2026-10-06T03:00:00.000Z', value: cfg })
    expect(ch.alarms.get('sync')).toEqual({ periodInMinutes: 2, delayInMinutes: 2 })
  })

  test('a failed refresh keeps the cached config, or gives null when there is none', async () => {
    const api = apiStub({ config: async () => { throw new NetworkError('offline') } })
    expect(await service(api).refresh()).toBeNull()
    await cached('2026-10-01T00:00:00.000Z')
    expect((await service(api).refresh())?.tracking.segmentSeconds).toBe(60)
    expect((await store.getKv('config'))?.fetchedAt).toBe('2026-10-01T00:00:00.000Z')
  })

  test('T28: refreshIfStale refetches only after a day', async () => {
    await cached(new Date(clock.now().getTime() - 23 * HOUR).toISOString())
    const api = apiStub()
    await service(api).refreshIfStale()
    expect(api.count('config')).toBe(0)
    clock.advance(2 * HOUR)
    await service(api).refreshIfStale()
    expect(api.count('config')).toBe(1)
  })

  test('ensureSchedule follows the cached interval and does nothing without config', async () => {
    const api = apiStub({ config: async () => { throw new NetworkError('offline') } })
    await service(api).ensureSchedule()
    expect(ch.alarms.size).toBe(0)
    await cached('2026-10-06T02:00:00.000Z', 5)
    await service(api).ensureSchedule()
    expect(ch.alarms.get('sync')).toEqual({ periodInMinutes: 5, delayInMinutes: 5 })
  })
})
