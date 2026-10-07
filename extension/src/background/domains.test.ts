import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { installChrome, type ChromeMock } from '../../test/chrome'
import { testDb } from '../../test/helpers'
import { parseRemoteConfig } from '../shared/config'
import { wireConfig } from '../../test/fixtures'
import type { Db } from './db'
import { DomainService, SCRIPT_PREFIX } from './domains'

describe('DomainService', () => {
  let store: Db
  let ch: ChromeMock
  let svc: DomainService

  beforeEach(() => {
    store = testDb()
    ch = installChrome()
    svc = new DomainService({ db: store })
  })

  afterEach(() => vi.unstubAllGlobals())

  test('T31: enabling asks for permission first, then registers the content script', async () => {
    expect(await svc.enable('https://www.Example.com/post')).toEqual({ ok: true, domain: 'example.com' })
    expect(ch.events[0]).toBe('request https://example.com/* https://*.example.com/*')
    expect(ch.scripts.get(`${SCRIPT_PREFIX}example.com`)).toEqual(['https://example.com/*', 'https://*.example.com/*'])
    expect(await store.getKv('enabled_domains')).toEqual(['example.com'])
  })

  test('invalid input never prompts', async () => {
    expect(await svc.enable('not a domain')).toEqual({ ok: false, reason: 'invalid' })
    expect(ch.events).toEqual([])
  })

  test('a declined prompt registers and saves nothing', async () => {
    ch.allowPermission = false
    expect(await svc.enable('example.com')).toEqual({ ok: false, reason: 'permission_denied' })
    expect(ch.scripts.size).toBe(0)
    expect(await store.getKv('enabled_domains')).toBeUndefined()
  })

  test('enabling twice keeps one entry', async () => {
    await svc.enable('example.com')
    expect(await svc.enable('example.com')).toEqual({ ok: false, reason: 'already_enabled' })
    expect(await store.getKv('enabled_domains')).toEqual(['example.com'])
  })

  test('I4: the permission prompt is requested synchronously, inside the user gesture', async () => {
    const p = svc.enable('example.com') // deliberately not awaited: no storage read can come before the prompt
    expect(ch.events[0]).toBe('request https://example.com/* https://*.example.com/*')
    await p
  })

  test('M3: Medium and Freedium hosts are always tracked, so enabling them is refused before any prompt', async () => {
    const p = svc.enable('medium.com')
    expect(ch.events).toEqual([])
    expect(await p).toEqual({ ok: false, reason: 'already_tracked' })
    expect(await svc.enable('www.medium.com')).toEqual({ ok: false, reason: 'already_tracked' })
    expect(ch.events).toEqual([])
    expect(await store.getKv('enabled_domains')).toBeUndefined()
  })

  test('M3: disabling removes the list entry even when removing the permission fails', async () => {
    await svc.enable('example.com')
    ch.chrome.permissions.remove.mockRejectedValueOnce(new Error('boom'))
    await svc.disable('example.com')
    expect(await store.getKv('enabled_domains')).toEqual([])
    expect(ch.scripts.size).toBe(0)
  })

  test('T31: disabling unregisters the script and drops the permission', async () => {
    await svc.enable('example.com')
    await svc.enable('other.org')
    await svc.disable('example.com')
    expect([...ch.scripts.keys()]).toEqual([`${SCRIPT_PREFIX}other.org`])
    expect(ch.granted.has('https://example.com/*')).toBe(false)
    expect(await store.getKv('enabled_domains')).toEqual(['other.org'])
  })

  test('deny and undeny', async () => {
    expect(await svc.deny('Blog.Example.com')).toEqual({ ok: true, domain: 'blog.example.com' })
    expect(await svc.deny('??')).toEqual({ ok: false, reason: 'invalid' })
    expect((await svc.lists()).denied).toEqual(['blog.example.com'])
    await svc.undeny('blog.example.com')
    expect((await svc.lists()).denied).toEqual([])
  })

  test('T31: plan never tracks a denied domain, even if enabled', async () => {
    const cfg = parseRemoteConfig(wireConfig)
    await svc.enable('example.com')
    expect(await svc.plan('blog.example.com', cfg)).toBe('generic')
    await svc.deny('example.com')
    expect(await svc.plan('blog.example.com', cfg)).toBeNull()
    expect(await svc.plan('medium.com', cfg)).toBe('freedium-medium')
    expect(await svc.plan('unrelated.net', cfg)).toBeNull()
  })

  test('reconcile restores missing scripts and removes stale generic ones only', async () => {
    await store.setKv('enabled_domains', ['example.com'])
    ch.scripts.set(`${SCRIPT_PREFIX}gone.org`, ['https://gone.org/*'])
    ch.scripts.set('something-else', ['https://x.org/*'])
    await svc.reconcile()
    expect([...ch.scripts.keys()].sort()).toEqual([`${SCRIPT_PREFIX}example.com`, 'something-else'])
  })

  test('reconcile with nothing stale unregisters nothing', async () => {
    await svc.enable('example.com')
    await svc.reconcile()
    expect(ch.chrome.scripting.unregisterContentScripts).not.toHaveBeenCalled()
  })

  describe('chrome.scripting', () => {
    test('T31: registers content.js for the domain, persisted across sessions', async () => {
      await svc.enable('example.com')
      expect(ch.chrome.scripting.registerContentScripts).toHaveBeenCalledWith([{
        id: 'generic:example.com', matches: ['https://example.com/*', 'https://*.example.com/*'], js: ['content.js'],
        runAt: 'document_idle', persistAcrossSessions: true,
      }])
    })

    test('re-registering an existing id updates it instead of throwing', async () => {
      ch.scripts.set('generic:example.com', ['stale'])
      await svc.enable('example.com')
      expect(ch.chrome.scripting.registerContentScripts).not.toHaveBeenCalled()
      expect(ch.chrome.scripting.updateContentScripts).toHaveBeenCalledTimes(1)
    })

    test('unregister skips ids Chrome does not know (it would throw)', async () => {
      ch.scripts.set('generic:a.com', [])
      await store.setKv('enabled_domains', ['a.com', 'b.com'])
      await svc.disable('b.com')
      expect(ch.chrome.scripting.unregisterContentScripts).not.toHaveBeenCalled()
      await svc.disable('a.com')
      expect(ch.chrome.scripting.unregisterContentScripts).toHaveBeenCalledWith({ ids: ['generic:a.com'] })
    })
  })

  test('permissions are requested and removed by origin patterns', async () => {
    await svc.enable('example.com')
    expect(ch.chrome.permissions.request).toHaveBeenCalledWith({ origins: ['https://example.com/*', 'https://*.example.com/*'] })
    await svc.disable('example.com')
    expect(ch.chrome.permissions.remove).toHaveBeenCalledWith({ origins: ['https://example.com/*', 'https://*.example.com/*'] })
  })
})
