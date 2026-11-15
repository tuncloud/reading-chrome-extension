import { beforeEach, describe, expect, test } from 'vitest'
import { apiStub, captureLog, clock as makeClock, record, segment, testDb } from '../../test/helpers'
import { ApiError, NetworkError } from '../shared/errors'
import type { History, HistoryArticle } from '../shared/types'
import type { Db } from './db'
import { RestoreService } from './restore'

const article = (url: string, over: Partial<HistoryArticle> = {}): HistoryArticle => ({
  canonicalUrl: url, source: 'medium', title: `T ${url}`, author: 'Jane Doe', siteName: 'Better Programming', wordCount: 1000,
  publishedAt: '2026-09-30', totalSeconds: 60, maxProgress: 0.2, lastReadAt: '2026-10-06T03:07:00Z', completed: false,
  notionUrl: null, ...over,
})

const history = (seconds: number, date = '2026-10-06'): History => ({
  articles: [article('https://e.com/a'), article('https://e.com/b', { lastReadAt: '2026-10-05T03:07:00Z' })],
  today: { date, seconds },
})

describe('RestoreService.run', () => {
  let store: Db
  const clock = makeClock('2026-10-06T03:12:00.000Z', 'Asia/Ho_Chi_Minh')

  beforeEach(async () => {
    store = testDb()
    await store.setKv('device_token', 'rt_x')
  })

  const service = (api: ReturnType<typeof apiStub>, log = captureLog()) => new RestoreService({ db: store, api, clock, log })

  test('stores history and today carry after connect', async () => {
    const api = apiStub({ history: async () => history(1800) })
    expect(await service(api).run()).toBe(true)
    expect((await store.recentArticles(5)).map((a) => a.canonicalUrl)).toEqual(['https://e.com/a', 'https://e.com/b'])
    expect(await store.getKv('today_carry')).toEqual({ date: '2026-10-06', seconds: 1800 })
    expect(api.calls.filter((c) => c.method === 'history').map((c) => c.args)).toEqual([
      ['rt_x', { timeZone: 'Asia/Ho_Chi_Minh', limit: 50 }],
    ])
    expect(await store.countUnsynced()).toBe(0)
    expect(await store.segmentsSince('')).toEqual([])
  })

  test('subtracts what local segments already count for today', async () => {
    await record(store, segment('a', { startedAt: '2026-10-06T03:00:00.000Z', activeSeconds: 300 }))
    await service(apiStub({ history: async () => history(1800) })).run()
    expect((await store.getKv('today_carry'))?.seconds).toBe(1500)
    await service(apiStub({ history: async () => history(100) })).run()
    expect((await store.getKv('today_carry'))?.seconds).toBe(0)
  })

  test('keeps the server date, so a total for another date can be ignored', async () => {
    await service(apiStub({ history: async () => history(900, '2026-10-05') })).run()
    expect(await store.getKv('today_carry')).toEqual({ date: '2026-10-05', seconds: 900 })
  })

  test('restore failure leaves local data untouched and does not throw', async () => {
    await record(store, segment('a'))
    await store.setKv('today_carry', { date: '2026-10-06', seconds: 7 })
    const before = await store.recentArticles(10)
    const failures: (() => Promise<History>)[] = [
      async () => { throw new NetworkError('offline') },
      async () => { throw new ApiError(500, 'internal', 'boom') },
      async () => ({ today: { date: '2026-10-06', seconds: 5 } }) as unknown as History,
      async () => ({ articles: 'nope', today: { date: '2026-10-06', seconds: 5 } }) as unknown as History,
    ]
    for (const history of failures) {
      expect(await service(apiStub({ history })).run()).toBe(false)
      expect(await store.recentArticles(10)).toEqual(before)
      expect(await store.getKv('today_carry')).toEqual({ date: '2026-10-06', seconds: 7 })
    }
  })

  test('does nothing without a device token', async () => {
    await store.deleteKv('device_token')
    const api = apiStub()
    expect(await service(api).run()).toBe(false)
    expect(api.count('history')).toBe(0)
  })
})
