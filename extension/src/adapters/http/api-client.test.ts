import { describe, expect, test } from 'vitest'
import { wireConfig } from '../../../test/fixtures'
import { ApiError, NetworkError } from '../../shared/errors'
import { HttpApi, type Fetch } from './api-client'

interface Call {
  url: string
  method: string
  headers: Record<string, string>
  body: unknown
}

function stub(respond: (call: Call) => Response | Promise<Response>): { api: HttpApi; calls: Call[] } {
  const calls: Call[] = []
  const fetch: Fetch = async (url, init) => {
    const call: Call = {
      url,
      method: init?.method ?? 'GET',
      headers: Object.fromEntries(new Headers(init?.headers).entries()),
      body: init?.body === undefined ? undefined : JSON.parse(String(init.body)),
    }
    calls.push(call)
    return respond(call)
  }
  return { api: new HttpApi({ baseUrl: 'https://api.example.com/', fetch }), calls }
}

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } })

const wireInstallation = { id: 'b1', workspace_name: "Tuan's Notion", status: 'active', library_url: 'https://www.notion.so/2a1b' }
const installation = { id: 'b1', workspaceName: "Tuan's Notion", status: 'active', setupError: '', libraryUrl: 'https://www.notion.so/2a1b' }

describe('HttpApi requests', () => {
  test('oauthStartUrl carries ext_redirect and the S256 challenge (§2.1)', () => {
    const { api } = stub(() => json(200, {}))
    const u = new URL(api.oauthStartUrl({ extRedirect: 'https://abc.chromiumapp.org/', codeChallenge: 'E9Mel' }))
    expect(u.origin + u.pathname).toBe('https://api.example.com/v1/oauth/notion/start')
    expect(Object.fromEntries(u.searchParams)).toEqual({
      ext_redirect: 'https://abc.chromiumapp.org/', code_challenge: 'E9Mel', code_challenge_method: 'S256',
    })
  })

  test('config is unauthenticated and parsed (§2.10)', async () => {
    const { api, calls } = stub(() => json(200, wireConfig))
    const cfg = await api.config()
    expect(cfg.tracking.idleSeconds).toBe(60)
    expect(calls[0]).toMatchObject({ url: 'https://api.example.com/v1/config', method: 'GET' })
    expect(calls[0]?.headers.authorization).toBeUndefined()
  })

  test('exchangeCode posts snake_case and maps the grant (§2.3)', async () => {
    const { api, calls } = stub(() => json(200, {
      device_token: 'rt_abc', device_id: 'd1', user: { id: 'u1', name: 'Tuan Do' }, installation: wireInstallation,
    }))
    const grant = await api.exchangeCode({ code: 'c', codeVerifier: 'v', deviceName: 'Chrome on macOS' })
    expect(calls[0]).toMatchObject({
      url: 'https://api.example.com/v1/auth/token', method: 'POST',
      body: { code: 'c', code_verifier: 'v', device_name: 'Chrome on macOS' },
    })
    expect(calls[0]?.headers['content-type']).toBe('application/json')
    expect(grant).toEqual({ deviceToken: 'rt_abc', deviceId: 'd1', user: { id: 'u1', name: 'Tuan Do' }, installation })
  })

  test('me sends the bearer token and maps a needs_setup installation (§2.5)', async () => {
    const { api, calls } = stub(() => json(200, {
      user: { id: 'u1', name: 'T' }, device: { id: 'd1', name: 'Mac' },
      installation: { id: 'b1', workspace_name: 'W', status: 'needs_setup', setup_error: 'template_not_found', library_url: null },
    }))
    const me = await api.me('rt_x')
    expect(calls[0]?.headers.authorization).toBe('Bearer rt_x')
    expect(me.installation).toEqual({ id: 'b1', workspaceName: 'W', status: 'needs_setup', setupError: 'template_not_found', libraryUrl: null })
  })

  test('me maps a null installation', async () => {
    const { api } = stub(() => json(200, { user: { id: 'u1', name: 'T' }, device: { id: 'd1', name: 'Mac' }, installation: null }))
    expect((await api.me('rt_x')).installation).toBeNull()
  })

  test('push sends the exact §2.8 body and maps the result', async () => {
    const { api, calls } = stub(() => json(200, { accepted: ['s1'], duplicate: [], retry: ['s2'], rejected: [{ id: 's3', reason: 'started_at_out_of_range' }] }))
    const res = await api.push('rt_x', {
      clientTime: '2026-10-06T03:12:00.000Z', tz: 'Asia/Ho_Chi_Minh',
      articles: [{ canonicalUrl: 'https://medium.com/p/8f3c2a1b9d4e', source: 'medium', title: 'T', author: 'A', siteName: 'S',
        wordCount: 1820, publishedAt: '2026-09-30', originalUrl: 'https://freedium.cfd/x' }],
      segments: [{ id: 's1', sessionId: 'c3', seq: 0, canonicalUrl: 'https://medium.com/p/8f3c2a1b9d4e',
        startedAt: '2026-10-06T03:01:05.000Z', endedAt: '2026-10-06T03:02:10.000Z', activeSeconds: 58, maxProgress: 0.42 }],
    })
    expect(calls[0]).toMatchObject({ url: 'https://api.example.com/v1/sync/push', method: 'POST' })
    expect(calls[0]?.body).toEqual({
      client_time: '2026-10-06T03:12:00.000Z', tz: 'Asia/Ho_Chi_Minh',
      articles: [{ canonical_url: 'https://medium.com/p/8f3c2a1b9d4e', source: 'medium', title: 'T', author: 'A', site_name: 'S',
        word_count: 1820, published_at: '2026-09-30', original_url: 'https://freedium.cfd/x' }],
      segments: [{ id: 's1', session_id: 'c3', seq: 0, canonical_url: 'https://medium.com/p/8f3c2a1b9d4e',
        started_at: '2026-10-06T03:01:05.000Z', ended_at: '2026-10-06T03:02:10.000Z', active_seconds: 58, max_progress: 0.42 }],
    })
    expect(res).toEqual({ accepted: ['s1'], duplicate: [], retry: ['s2'], rejected: [{ id: 's3', reason: 'started_at_out_of_range' }] })
  })

  test('push maps null arrays to empty arrays', async () => {
    const { api } = stub(() => json(200, { accepted: null, duplicate: null, retry: null, rejected: null }))
    const res = await api.push('rt_x', { clientTime: '', tz: 'UTC', articles: [], segments: [] })
    expect(res).toEqual({ accepted: [], duplicate: [], retry: [], rejected: [] })
  })

  test('articlesStatus maps items keyed by URL (§2.9)', async () => {
    const { api, calls } = stub(() => json(200, { items: { 'https://e.com/a': {
      total_seconds: 734, max_progress: 0.97, sessions: 2, completed: true, last_read_at: '2026-10-06T03:20:00Z', notion_url: null,
    } } }))
    const items = await api.articlesStatus('rt_x', ['https://e.com/a'])
    expect(calls[0]?.body).toEqual({ canonical_urls: ['https://e.com/a'] })
    expect(items).toEqual({ 'https://e.com/a': {
      totalSeconds: 734, maxProgress: 0.97, sessions: 2, completed: true, lastReadAt: '2026-10-06T03:20:00Z', notionUrl: null,
    } })
  })

  test('setupPages encodes q; setup posts parent_page_id (§2.6, §2.7)', async () => {
    const { api, calls } = stub((c) => c.url.includes('/pages')
      ? json(200, { pages: [{ id: 'p1', title: 'Reading', url: 'https://www.notion.so/p1' }] })
      : json(200, wireInstallation))
    expect(await api.setupPages('rt_x', 'Read & notes')).toEqual([{ id: 'p1', title: 'Reading', url: 'https://www.notion.so/p1' }])
    expect(calls[0]?.url).toBe('https://api.example.com/v1/setup/pages?q=Read+%26+notes')
    expect(await api.setup('rt_x', 'p1')).toEqual(installation)
    expect(calls[1]).toMatchObject({ url: 'https://api.example.com/v1/setup', method: 'POST', body: { parent_page_id: 'p1' } })
  })

  test('logout (204) and deleteAccount (202) resolve to undefined (§2.17)', async () => {
    const { api, calls } = stub((c) => new Response(null, { status: c.method === 'DELETE' ? 202 : 204 }))
    await expect(api.logout('rt_x')).resolves.toBeUndefined()
    await expect(api.deleteAccount('rt_x')).resolves.toBeUndefined()
    expect(calls.map((c) => `${c.method} ${c.url}`)).toEqual([
      'POST https://api.example.com/v1/auth/logout', 'DELETE https://api.example.com/v1/me',
    ])
  })
})

describe('HttpApi errors', () => {
  test('the §2.0 error body becomes ApiError', async () => {
    const { api } = stub(() => json(409, { error: { code: 'no_installation', message: 'connect Notion first' } }))
    const err = await api.push('rt_x', { clientTime: '', tz: 'UTC', articles: [], segments: [] }).catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err).toMatchObject({ status: 409, code: 'no_installation', message: 'connect Notion first' })
  })

  test('a 401 with §2.0 body becomes ApiError with code unauthorized', async () => {
    const { api } = stub(() => json(401, { error: { code: 'unauthorized', message: 'invalid token' } }))
    await expect(api.me('bad_token')).rejects.toMatchObject({ status: 401, code: 'unauthorized', message: 'invalid token' })
  })

  test('a non-JSON 502 becomes ApiError(502, internal)', async () => {
    const { api } = stub(() => new Response('<html>bad gateway</html>', { status: 502 }))
    await expect(api.me('rt_x')).rejects.toMatchObject({ status: 502, code: 'internal' })
  })

  test('a non-JSON 404 becomes ApiError(404, http_404)', async () => {
    const { api } = stub(() => new Response('nope', { status: 404 }))
    await expect(api.me('rt_x')).rejects.toMatchObject({ status: 404, code: 'http_404' })
  })

  test('a failed fetch becomes NetworkError', async () => {
    const api = new HttpApi({ baseUrl: 'https://api.example.com', fetch: async () => { throw new TypeError('Failed to fetch') } })
    await expect(api.config()).rejects.toBeInstanceOf(NetworkError)
  })

  test('a timeout becomes NetworkError', async () => {
    const api = new HttpApi({
      baseUrl: 'https://api.example.com', timeoutMs: 5,
      fetch: (_url, init) => new Promise((_, reject) => init?.signal?.addEventListener('abort', () => reject(init.signal?.reason))),
    })
    await expect(api.config()).rejects.toBeInstanceOf(NetworkError)
  })

  test('a body-read error becomes NetworkError', async () => {
    const api = new HttpApi({
      baseUrl: 'https://api.example.com',
      fetch: () => Promise.resolve({ ok: true, status: 200, text: () => Promise.reject(new DOMException('aborted', 'AbortError')) } as unknown as Response),
    })
    await expect(api.config()).rejects.toBeInstanceOf(NetworkError)
  })
})
