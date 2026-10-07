import { beforeEach, describe, expect, test, vi } from 'vitest'
import { FakeApi, FakeAuthFlow, FakePkce, INSTALLATION, MemoryLog, MemoryStore } from '../../test/fakes'
import { ApiError, NetworkError } from '../shared/errors'
import { SessionService } from './session'

describe('SessionService', () => {
  let store: MemoryStore
  let flow: FakeAuthFlow
  let afterConnect: () => void

  beforeEach(() => {
    store = new MemoryStore()
    flow = new FakeAuthFlow()
    afterConnect = vi.fn<() => void>()
  })

  const service = (api: FakeApi) => new SessionService({
    store, api, authFlow: flow, pkce: new FakePkce(), deviceName: () => 'Chrome on macOS', log: new MemoryLog(), afterConnect,
  })

  const nothingStored = async () => {
    for (const k of ['device_token', 'device_id', 'user', 'installation'] as const) expect(await store.getKv(k)).toBeUndefined()
  }

  describe('connect (T29)', () => {
    test('PKCE start → redirect code → token exchange → stored', async () => {
      const api = new FakeApi()
      const res = await service(api).connect()
      expect(res).toEqual({ ok: true, installation: INSTALLATION })
      expect(api.calls[0]).toEqual({
        method: 'oauthStartUrl', args: [{ extRedirect: 'https://ext.chromiumapp.org/', codeChallenge: 'challenge-of-v' }],
      })
      expect(flow.launched).toEqual(['https://api.example.com/v1/oauth/notion/start?code_challenge=challenge-of-v'])
      expect(api.calls.find((c) => c.method === 'exchangeCode')?.args[0]).toEqual({
        code: 'one-time', codeVerifier: 'v'.repeat(64), deviceName: 'Chrome on macOS',
      })
      expect(await store.getKv('device_token')).toBe('rt_new')
      expect(await store.getKv('device_id')).toBe('d1')
      expect(await store.getKv('user')).toEqual({ id: 'u1', name: 'Tuan Do' })
      expect(await store.getKv('installation')).toEqual(INSTALLATION)
      expect(api.calls.find((c) => c.method === 'me')?.args).toEqual(['rt_new'])
      expect(afterConnect).toHaveBeenCalledTimes(1)
    })

    test('I2: connecting clears stale sync backoff', async () => {
      await store.setKv('sync_failures', 6)
      await store.setKv('sync_backoff_until', '2099-01-01T00:00:00.000Z')
      await service(new FakeApi()).connect()
      expect(await store.getKv('sync_failures')).toBeUndefined()
      expect(await store.getKv('sync_backoff_until')).toBeUndefined()
    })

    test('user closes the window → cancelled, nothing stored', async () => {
      flow.respond = async () => { throw new Error('The user did not approve access.') }
      const api = new FakeApi()
      expect(await service(api).connect()).toEqual({ ok: false, reason: 'cancelled' })
      expect(api.count('exchangeCode')).toBe(0)
      await nothingStored()
      expect(afterConnect).not.toHaveBeenCalled()
    })

    test('error=access_denied → that reason, nothing stored', async () => {
      flow.respond = async () => 'https://ext.chromiumapp.org/?error=access_denied'
      expect(await service(new FakeApi()).connect()).toEqual({ ok: false, reason: 'access_denied' })
      await nothingStored()
    })

    test('a redirect without a code → invalid_response', async () => {
      flow.respond = async () => 'https://ext.chromiumapp.org/'
      expect(await service(new FakeApi()).connect()).toEqual({ ok: false, reason: 'invalid_response' })
      await nothingStored()
    })

    test('a failed exchange → the API error code, nothing stored', async () => {
      const api = new FakeApi({ exchangeCode: async () => { throw new ApiError(400, 'invalid_grant', 'bad code') } })
      expect(await service(api).connect()).toEqual({ ok: false, reason: 'invalid_grant' })
      await nothingStored()
    })

    test('a network failure during the exchange → network_error', async () => {
      const api = new FakeApi({ exchangeCode: async () => { throw new NetworkError('offline') } })
      expect(await service(api).connect()).toEqual({ ok: false, reason: 'network_error' })
    })

    test('malformed redirect URL → invalid_response', async () => {
      flow.respond = async () => 'not a url'
      expect(await service(new FakeApi()).connect()).toEqual({ ok: false, reason: 'invalid_response' })
      await nothingStored()
    })

    test('post-exchange refresh returns 401 → unauthorized, nothing stored', async () => {
      const log = new MemoryLog()
      const api = new FakeApi({ me: async () => { throw new ApiError(401, 'unauthorized', 'revoked') } })
      const svc = new SessionService({
        store, api, authFlow: flow, pkce: new FakePkce(), deviceName: () => 'Chrome on macOS', log, afterConnect,
      })
      expect(await svc.connect()).toEqual({ ok: false, reason: 'unauthorized' })
      await nothingStored()
      expect(afterConnect).not.toHaveBeenCalled()
    })
  })

  describe('state and refresh', () => {
    test('state is disconnected without a token, connected with one', async () => {
      const svc = service(new FakeApi())
      expect(await svc.state()).toEqual({ kind: 'disconnected' })
      await store.setKv('device_token', 'rt_x')
      expect(await svc.state()).toEqual({ kind: 'connected', user: null, installation: null })
    })

    test('refresh stores /v1/me', async () => {
      await store.setKv('device_token', 'rt_x')
      const api = new FakeApi({ me: async () => ({
        user: { id: 'u1', name: 'T' }, device: { id: 'd1', name: 'Mac' }, installation: { ...INSTALLATION, status: 'revoked' },
      }) })
      expect(await service(api).refresh()).toEqual({
        kind: 'connected', user: { id: 'u1', name: 'T' }, installation: { ...INSTALLATION, status: 'revoked' },
      })
      expect((await store.getKv('installation'))?.status).toBe('revoked')
    })

    test('refresh on 401 disconnects', async () => {
      await store.setKv('device_token', 'rt_x')
      const api = new FakeApi({ me: async () => { throw new ApiError(401, 'unauthorized', 'revoked') } })
      expect(await service(api).refresh()).toEqual({ kind: 'disconnected' })
      expect(await store.getKv('device_token')).toBeUndefined()
    })

    test('refresh offline keeps the cached state', async () => {
      await store.setKv('device_token', 'rt_x')
      await store.setKv('installation', INSTALLATION)
      const api = new FakeApi({ me: async () => { throw new NetworkError('offline') } })
      expect(await service(api).refresh()).toEqual({ kind: 'connected', user: null, installation: INSTALLATION })
    })

    test('refresh on 500 logs the error and keeps the cached state', async () => {
      await store.setKv('device_token', 'rt_x')
      await store.setKv('installation', INSTALLATION)
      const log = new MemoryLog()
      const api = new FakeApi({ me: async () => { throw new ApiError(500, 'internal', 'boom') } })
      const svc = new SessionService({
        store, api, authFlow: flow, pkce: new FakePkce(), deviceName: () => 'Chrome on macOS', log, afterConnect,
      })
      expect(await svc.refresh()).toEqual({ kind: 'connected', user: null, installation: INSTALLATION })
      expect(log.lines.some((line) => line.includes('refresh failed'))).toBe(true)
    })
  })

  describe('logout and delete (T31, §2.17)', () => {
    beforeEach(async () => {
      await store.setKv('device_token', 'rt_x')
      await store.setKv('device_id', 'd1')
      await store.setKv('installation', INSTALLATION)
      await store.setKv('enabled_domains', ['example.com'])
      store.articleRows.set('https://e.com/a', {
        canonicalUrl: 'https://e.com/a', source: 'web', title: 'A', author: '', siteName: '', wordCount: 0,
        publishedAt: '', originalUrl: 'https://e.com/a', totalSeconds: 60, maxProgress: 0.5, lastReadAt: '2026-10-06T03:00:00.000Z',
      })
    })

    test('logout calls the API and disconnects locally even if it fails', async () => {
      const api = new FakeApi({ logout: async () => { throw new NetworkError('offline') } })
      await service(api).logout()
      expect(api.calls.map((c) => c.method)).toEqual(['logout'])
      await nothingStored()
      expect(store.articleRows.size).toBe(1)
    })

    test('deleteAccount clears auth and local reading data after the server accepts', async () => {
      const api = new FakeApi()
      await service(api).deleteAccount()
      expect(api.count('deleteAccount')).toBe(1)
      await nothingStored()
      expect(store.articleRows.size).toBe(0)
      expect(await store.getKv('enabled_domains')).toEqual(['example.com'])
    })

    test('M2: deleteAccount treats a 401 as the account already gone', async () => {
      const api = new FakeApi({ deleteAccount: async () => { throw new ApiError(401, 'unauthorized', 'revoked') } })
      await service(api).deleteAccount()
      await nothingStored()
      expect(store.articleRows.size).toBe(0)
    })

    test('deleteAccount keeps everything when the server call fails', async () => {
      const api = new FakeApi({ deleteAccount: async () => { throw new ApiError(500, 'internal', 'boom') } })
      await expect(service(api).deleteAccount()).rejects.toBeInstanceOf(ApiError)
      expect(await store.getKv('device_token')).toBe('rt_x')
      expect(store.articleRows.size).toBe(1)
    })
  })
})
