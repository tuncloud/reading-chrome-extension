import { afterEach, describe, expect, test, vi } from 'vitest'
import { wireConfig } from '../../test/fixtures'
import { installChrome } from '../../test/chrome'
import { createServices } from './services'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.unstubAllEnvs()
})

test('createServices registers no chrome listeners and creates no alarms', () => {
  const ch = installChrome()
  vi.stubEnv('VITE_API_BASE', 'http://localhost:8080')
  createServices('test')
  expect(Object.values(ch.listeners).flat()).toHaveLength(0)
  expect(ch.chrome.alarms.create).not.toHaveBeenCalled()
})

const json = (body: unknown) => new Response(JSON.stringify(body), { status: 200, headers: { 'content-type': 'application/json' } })

describe('connect wiring', () => {
  const installation = { id: 'b1', workspace_name: 'W', status: 'active', library_url: null }
  const user = { id: 'u1', name: 'T' }

  async function connectWith(historyResponse: () => Response) {
    installChrome()
    vi.stubEnv('VITE_API_BASE', 'http://localhost:8080')
    const urls: string[] = []
    vi.stubGlobal('fetch', async (url: string) => {
      urls.push(new URL(url).pathname)
      if (url.includes('/v1/auth/token')) return json({ device_token: 'rt_new', device_id: 'd1', user, installation })
      if (url.includes('/v1/me')) return json({ user, device: { id: 'd1', name: 'Mac' }, installation })
      if (url.includes('/v1/history')) return historyResponse()
      if (url.includes('/v1/config')) return json(wireConfig)
      return json({ accepted: [], duplicate: [], retry: [], rejected: [] })
    })
    const svc = createServices('test')
    const sync = vi.spyOn(svc.sync, 'run')
    const res = await svc.session.connect()
    await vi.waitFor(() => expect(sync).toHaveBeenCalledTimes(1))
    return { svc, res, urls, sync }
  }

  test('after a successful connect, history is requested once, restored, and a sync starts', async () => {
    const { svc, res, urls } = await connectWith(() => json({
      articles: [{
        canonical_url: 'https://e.com/a', source: 'web', title: 'A', author: '', site_name: '', word_count: 0, published_at: '',
        total_seconds: 90, max_progress: 0.5, last_read_at: '2026-10-06T03:07:00Z', completed: false, notion_url: null,
      }],
      today: { date: '2026-10-06', seconds: 90 },
    }))
    expect(res).toMatchObject({ ok: true })
    expect(urls.filter((u) => u === '/v1/history')).toHaveLength(1)
    await vi.waitFor(async () => expect((await svc.db.articles(['https://e.com/a']))[0]?.totalSeconds).toBe(90))
  })

  test('when history fails, connect still resolves ok and a sync still starts', async () => {
    const { res, urls } = await connectWith(() => new Response('boom', { status: 500 }))
    expect(res).toMatchObject({ ok: true })
    expect(urls.filter((u) => u === '/v1/history')).toHaveLength(1)
  })
})
