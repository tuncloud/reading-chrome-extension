import { beforeEach, describe, expect, test } from 'vitest'
import { FakeApi, FixedClock, INSTALLATION, MemoryStore } from '../../test/fakes'
import { record, segment } from '../../test/store-contract'
import { roundMinutes } from '../domain/reading'
import { NetworkError } from './errors'
import { buildPopupModel } from './popup-model'
import type { SessionState } from './session'

const connected = (status: string, extra: object = {}): SessionState => ({
  kind: 'connected', user: { id: 'u1', name: 'T' }, installation: { ...INSTALLATION, status, ...extra },
})

describe('buildPopupModel', () => {
  let store: MemoryStore
  // 10:00 UTC is 17:00 in Ho Chi Minh, so "today" is 2026-10-06 local.
  const clock = new FixedClock(new Date('2026-10-06T10:00:00.000Z'), 'Asia/Ho_Chi_Minh')

  beforeEach(async () => {
    store = new MemoryStore()
    await store.setKv('device_token', 'rt_x')
  })

  const build = (session: SessionState, current: string | null = null, api = new FakeApi()) =>
    buildPopupModel({ store, api, clock }, session, current)

  test('T30: each installation status has its own state', async () => {
    expect(await build({ kind: 'disconnected' })).toEqual({ kind: 'disconnected' })
    expect(await build({ kind: 'connected', user: null, installation: null })).toEqual({ kind: 'disconnected' })
    expect(await build(connected('needs_setup', { setupError: 'template_not_found' }))).toEqual({
      kind: 'needs_setup', workspaceName: "Tuan's Notion", setupError: 'template_not_found',
    })
    expect(await build(connected('revoked'))).toEqual({ kind: 'revoked', workspaceName: "Tuan's Notion" })
    expect((await build(connected('active'))).kind).toBe('active')
  })

  test("T30: today's minutes are today's local segments / 60", async () => {
    await record(store, segment('yesterday', { startedAt: '2026-10-05T16:59:00.000Z', activeSeconds: 600 })) // 23:59 local, 5th
    await record(store, segment('a', { startedAt: '2026-10-05T17:00:00.000Z', activeSeconds: 125 })) // 00:00 local, 6th
    await record(store, segment('b', { startedAt: '2026-10-06T09:00:00.000Z', activeSeconds: 200 }))
    const m = await build(connected('active'))
    expect(m.kind === 'active' && m.todayMinutes).toBe(roundMinutes(325))
  })

  test('current article, last 5 and the library link; server status enriches them', async () => {
    for (let i = 0; i < 7; i++) {
      await record(store, segment(`s${i}`, {
        canonicalUrl: `https://e.com/${i}`, activeSeconds: 60 * (i + 1), maxProgress: 0.1 * i,
        endedAt: `2026-10-06T0${i}:00:00.000Z`,
      }), { canonicalUrl: '', source: 'web', title: `Article ${i}`, author: '', siteName: '', wordCount: 0, publishedAt: '', originalUrl: '' })
    }
    const api = new FakeApi({
      articlesStatus: async () => ({
        'https://e.com/6': { totalSeconds: 900, maxProgress: 0.95, sessions: 3, completed: true, lastReadAt: '', notionUrl: 'https://www.notion.so/6' },
      }),
    })
    const m = await build(connected('active'), 'https://e.com/6', api)
    if (m.kind !== 'active') throw new Error(m.kind)
    expect(m.current).toEqual({
      url: 'https://e.com/6', title: 'Article 6', minutes: 15, progressPercent: 95, completed: true, notionUrl: 'https://www.notion.so/6',
    })
    expect(m.recent.map((a) => a.title)).toEqual(['Article 6', 'Article 5', 'Article 4', 'Article 3', 'Article 2'])
    expect(m.recent[1]).toMatchObject({ minutes: 6, progressPercent: 50, completed: null, notionUrl: null })
    expect(m.libraryUrl).toBe('https://www.notion.so/lib')
    expect(m.unsynced).toBe(7)
    expect(api.calls.find((c) => c.method === 'articlesStatus')?.args[1]).toEqual([
      'https://e.com/6', 'https://e.com/5', 'https://e.com/4', 'https://e.com/3', 'https://e.com/2',
    ])
  })

  test('offline: local values only', async () => {
    await record(store, segment('a'))
    const api = new FakeApi({ articlesStatus: async () => { throw new NetworkError('offline') } })
    const m = await build(connected('active'), 'https://medium.com/p/8f3c2a1b9d4e', api)
    if (m.kind !== 'active') throw new Error(m.kind)
    expect(m.current).toMatchObject({ minutes: 1, progressPercent: 50, completed: null })
  })

  test('a page read only on another device still shows from the server', async () => {
    const api = new FakeApi({
      articlesStatus: async () => ({ 'https://e.com/x': { totalSeconds: 120, maxProgress: 0.2, sessions: 1, completed: false, lastReadAt: '', notionUrl: null } }),
    })
    const m = await build(connected('active'), 'https://e.com/x', api)
    expect(m.kind === 'active' && m.current).toEqual({
      url: 'https://e.com/x', title: 'https://e.com/x', minutes: 2, progressPercent: 20, completed: false, notionUrl: null,
    })
  })

  test('no current page → no current view, and no status call when nothing is listed', async () => {
    const api = new FakeApi()
    const m = await build(connected('active'), null, api)
    expect(m.kind === 'active' && m.current).toBeNull()
    expect(api.count('articlesStatus')).toBe(0)
  })
})
