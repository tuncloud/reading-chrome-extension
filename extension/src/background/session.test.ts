import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { installChrome, type ChromeMock } from '../../test/chrome'
import { apiStub, captureLog, INSTALLATION, record, segment, testDb } from '../../test/helpers'
import { ApiError, NetworkError } from '../shared/errors'
import type { Db } from './db'
import { deviceName } from './device'
import { challengeFor } from './pkce'
import { SessionService } from './session'

describe('SessionService', () => {
  let store: Db
  let ch: ChromeMock
  let afterConnect: () => void

  beforeEach(() => {
    store = testDb()
    ch = installChrome()
    afterConnect = vi.fn<() => void>()
  })

  afterEach(() => vi.unstubAllGlobals())

  const service = (api: ReturnType<typeof apiStub>, log = captureLog()) => new SessionService({ db: store, api, log, afterConnect })

  const articleCount = async () => (await store.recentArticles(10)).length

  const nothingStored = async () => {
    for (const k of ['device_token', 'device_id', 'user', 'installation'] as const) expect(await store.getKv(k)).toBeUndefined()
  }

  describe('connect (T29)', () => {
    test('PKCE start → redirect code → token exchange → stored', async () => {
      const api = apiStub()
      const res = await service(api).connect()
      expect(res).toEqual({ ok: true, installation: INSTALLATION })
      const exchange = api.calls.find((c) => c.method === 'exchangeCode')?.args[0] as { code: string; codeVerifier: string; deviceName: string }
      expect(exchange.codeVerifier).toMatch(/^[A-Za-z0-9\-._~]{64}$/)
      expect(exchange).toEqual({ code: 'one-time', codeVerifier: exchange.codeVerifier, deviceName: deviceName(navigator) })
      const challenge = await challengeFor(exchange.codeVerifier)
      expect(api.calls[0]).toEqual({
        method: 'oauthStartUrl', args: [{ extRedirect: 'https://ext.chromiumapp.org/', codeChallenge: challenge }],
      })
      expect(ch.events).toEqual([`launch https://api.example.com/v1/oauth/notion/start?code_challenge=${challenge}`])
      expect(ch.chrome.identity.launchWebAuthFlow).toHaveBeenCalledWith({
        url: `https://api.example.com/v1/oauth/notion/start?code_challenge=${challenge}`, interactive: true,
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
      await service(apiStub()).connect()
      expect(await store.getKv('sync_failures')).toBeUndefined()
      expect(await store.getKv('sync_backoff_until')).toBeUndefined()
    })

    test('user closes the window → cancelled, nothing stored', async () => {
      ch.redirect = async () => { throw new Error('The user did not approve access.') }
      const api = apiStub()
      expect(await service(api).connect()).toEqual({ ok: false, reason: 'cancelled' })
      expect(api.count('exchangeCode')).toBe(0)
      await nothingStored()
      expect(afterConnect).not.toHaveBeenCalled()
    })

    test('error=access_denied → that reason, nothing stored', async () => {
      ch.redirect = async () => 'https://ext.chromiumapp.org/?error=access_denied'
      expect(await service(apiStub()).connect()).toEqual({ ok: false, reason: 'access_denied' })
      await nothingStored()
    })

    test('a redirect without a code → invalid_response', async () => {
      ch.redirect = async () => 'https://ext.chromiumapp.org/'
      expect(await service(apiStub()).connect()).toEqual({ ok: false, reason: 'invalid_response' })
      await nothingStored()
    })

    test('a failed exchange → the API error code, nothing stored', async () => {
      const api = apiStub({ exchangeCode: async () => { throw new ApiError(400, 'invalid_grant', 'bad code') } })
      expect(await service(api).connect()).toEqual({ ok: false, reason: 'invalid_grant' })
      await nothingStored()
    })

    test('a network failure during the exchange → network_error', async () => {
      const api = apiStub({ exchangeCode: async () => { throw new NetworkError('offline') } })
      expect(await service(api).connect()).toEqual({ ok: false, reason: 'network_error' })
    })

    test('malformed redirect URL → invalid_response', async () => {
      ch.redirect = async () => 'not a url'
      expect(await service(apiStub()).connect()).toEqual({ ok: false, reason: 'invalid_response' })
      await nothingStored()
    })

    test('post-exchange refresh returns 401 → unauthorized, nothing stored', async () => {
      const api = apiStub({ me: async () => { throw new ApiError(401, 'unauthorized', 'revoked') } })
      expect(await service(api).connect()).toEqual({ ok: false, reason: 'unauthorized' })
      await nothingStored()
      expect(afterConnect).not.toHaveBeenCalled()
    })
  })

  describe('state and refresh', () => {
    test('state is disconnected without a token, connected with one', async () => {
      const svc = service(apiStub())
      expect(await svc.state()).toEqual({ kind: 'disconnected' })
      await store.setKv('device_token', 'rt_x')
      expect(await svc.state()).toEqual({ kind: 'connected', user: null, installation: null })
    })

    test('refresh stores /v1/me', async () => {
      await store.setKv('device_token', 'rt_x')
      const api = apiStub({ me: async () => ({
        user: { id: 'u1', name: 'T' }, device: { id: 'd1', name: 'Mac' }, installation: { ...INSTALLATION, status: 'revoked' },
      }) })
      expect(await service(api).refresh()).toEqual({
        kind: 'connected', user: { id: 'u1', name: 'T' }, installation: { ...INSTALLATION, status: 'revoked' },
      })
      expect((await store.getKv('installation'))?.status).toBe('revoked')
    })

    test('refresh on 401 disconnects', async () => {
      await store.setKv('device_token', 'rt_x')
      const api = apiStub({ me: async () => { throw new ApiError(401, 'unauthorized', 'revoked') } })
      expect(await service(api).refresh()).toEqual({ kind: 'disconnected' })
      expect(await store.getKv('device_token')).toBeUndefined()
    })

    test('refresh offline keeps the cached state', async () => {
      await store.setKv('device_token', 'rt_x')
      await store.setKv('installation', INSTALLATION)
      const api = apiStub({ me: async () => { throw new NetworkError('offline') } })
      expect(await service(api).refresh()).toEqual({ kind: 'connected', user: null, installation: INSTALLATION })
    })

    test('refresh on 500 logs the error and keeps the cached state', async () => {
      await store.setKv('device_token', 'rt_x')
      await store.setKv('installation', INSTALLATION)
      const log = captureLog()
      const api = apiStub({ me: async () => { throw new ApiError(500, 'internal', 'boom') } })
      expect(await service(api, log).refresh()).toEqual({ kind: 'connected', user: null, installation: INSTALLATION })
      expect(log.lines.some((line) => line.includes('refresh failed'))).toBe(true)
    })
  })

  describe('logout and delete (T31, §2.17)', () => {
    beforeEach(async () => {
      await store.setKv('device_token', 'rt_x')
      await store.setKv('device_id', 'd1')
      await store.setKv('installation', INSTALLATION)
      await store.setKv('enabled_domains', ['example.com'])
      await record(store, segment('s1', { canonicalUrl: 'https://e.com/a' }))
    })

    test('logout calls the API and disconnects locally even if it fails', async () => {
      const api = apiStub({ logout: async () => { throw new NetworkError('offline') } })
      await service(api).logout()
      expect(api.calls.map((c) => c.method)).toEqual(['logout'])
      await nothingStored()
      expect(await articleCount()).toBe(1)
    })

    test('deleteAccount clears auth and local reading data after the server accepts', async () => {
      const api = apiStub()
      await service(api).deleteAccount()
      expect(api.count('deleteAccount')).toBe(1)
      await nothingStored()
      expect(await articleCount()).toBe(0)
      expect(await store.getKv('enabled_domains')).toEqual(['example.com'])
    })

    test('M2: deleteAccount treats a 401 as the account already gone', async () => {
      const api = apiStub({ deleteAccount: async () => { throw new ApiError(401, 'unauthorized', 'revoked') } })
      await service(api).deleteAccount()
      await nothingStored()
      expect(await articleCount()).toBe(0)
    })

    test('deleteAccount keeps everything when the server call fails', async () => {
      const api = apiStub({ deleteAccount: async () => { throw new ApiError(500, 'internal', 'boom') } })
      await expect(service(api).deleteAccount()).rejects.toBeInstanceOf(ApiError)
      expect(await store.getKv('device_token')).toBe('rt_x')
      expect(await articleCount()).toBe(1)
    })
  })

  describe('chrome.identity', () => {
    test('an empty redirect from the auth window counts as cancelled', async () => {
      ch.redirect = async () => undefined
      const api = apiStub()
      expect(await service(api).connect()).toEqual({ ok: false, reason: 'cancelled' })
      expect(api.count('exchangeCode')).toBe(0)
    })

    test('launches an interactive window with the chromiumapp redirect', async () => {
      const api = apiStub()
      await service(api).connect()
      expect(ch.chrome.identity.launchWebAuthFlow).toHaveBeenCalledWith(expect.objectContaining({ interactive: true }))
      expect((api.calls[0]?.args[0] as { extRedirect: string }).extRedirect).toBe('https://ext.chromiumapp.org/')
    })
  })
})
