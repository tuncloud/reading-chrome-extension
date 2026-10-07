import { afterEach, describe, expect, test, vi } from 'vitest'
import { ChromeScheduler, onAlarm } from './alarms'
import { ChromeAuthFlow } from './identity'
import { ChromePermissions } from './permissions'
import { ChromeContentScripts } from './scripting'
import { activeTabId } from './tabs'

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('ChromeContentScripts', () => {
  function fake(registered: string[] = []) {
    const scripting = {
      registerContentScripts: vi.fn(async () => {}),
      updateContentScripts: vi.fn(async () => {}),
      unregisterContentScripts: vi.fn(async () => {}),
      getRegisteredContentScripts: vi.fn(async () => registered.map((id) => ({ id }))),
    }
    vi.stubGlobal('chrome', { scripting })
    return scripting
  }

  test('T31: registers content.js for the domain, persisted across sessions', async () => {
    const s = fake()
    await new ChromeContentScripts().register('generic:example.com', ['https://example.com/*'])
    expect(s.registerContentScripts).toHaveBeenCalledWith([{
      id: 'generic:example.com', matches: ['https://example.com/*'], js: ['content.js'], runAt: 'document_idle', persistAcrossSessions: true,
    }])
  })

  test('re-registering an existing id updates it instead of throwing', async () => {
    const s = fake(['generic:example.com'])
    await new ChromeContentScripts().register('generic:example.com', ['https://example.com/*'])
    expect(s.registerContentScripts).not.toHaveBeenCalled()
    expect(s.updateContentScripts).toHaveBeenCalledTimes(1)
  })

  test('unregister skips ids Chrome does not know (it would throw)', async () => {
    const s = fake(['generic:a.com'])
    await new ChromeContentScripts().unregister(['generic:a.com', 'generic:b.com'])
    expect(s.unregisterContentScripts).toHaveBeenCalledWith({ ids: ['generic:a.com'] })
    await new ChromeContentScripts().unregister(['generic:b.com'])
    expect(s.unregisterContentScripts).toHaveBeenCalledTimes(1)
  })

  test('registeredIds lists the ids', async () => {
    fake(['x', 'y'])
    expect(await new ChromeContentScripts().registeredIds()).toEqual(['x', 'y'])
  })
})

test('ChromeScheduler creates a repeating alarm; onAlarm passes the name', async () => {
  const listeners: ((a: { name: string }) => void)[] = []
  const alarms = { create: vi.fn(async () => {}), onAlarm: { addListener: (l: (a: { name: string }) => void) => listeners.push(l) } }
  vi.stubGlobal('chrome', { alarms })
  await new ChromeScheduler().every('sync', 2)
  expect(alarms.create).toHaveBeenCalledWith('sync', { periodInMinutes: 2, delayInMinutes: 2 })
  const seen: string[] = []
  onAlarm((name) => seen.push(name))
  listeners[0]!({ name: 'daily' })
  expect(seen).toEqual(['daily'])
})

test('ChromeAuthFlow uses the chromiumapp redirect and an interactive window', async () => {
  const identity = {
    getRedirectURL: () => 'https://abc.chromiumapp.org/',
    launchWebAuthFlow: vi.fn(async () => 'https://abc.chromiumapp.org/?code=x'),
  }
  vi.stubGlobal('chrome', { identity })
  const flow = new ChromeAuthFlow()
  expect(flow.redirectUrl()).toBe('https://abc.chromiumapp.org/')
  expect(await flow.launch('https://api/start')).toBe('https://abc.chromiumapp.org/?code=x')
  expect(identity.launchWebAuthFlow).toHaveBeenCalledWith({ url: 'https://api/start', interactive: true })
  identity.launchWebAuthFlow.mockResolvedValueOnce(undefined as unknown as string)
  await expect(flow.launch('https://api/start')).rejects.toThrow()
})

test('ChromePermissions passes origins through', async () => {
  const permissions = { request: vi.fn(async () => true), remove: vi.fn(async () => true) }
  vi.stubGlobal('chrome', { permissions })
  expect(await new ChromePermissions().request(['https://e.com/*'])).toBe(true)
  expect(permissions.request).toHaveBeenCalledWith({ origins: ['https://e.com/*'] })
  await new ChromePermissions().remove(['https://e.com/*'])
  expect(permissions.remove).toHaveBeenCalledWith({ origins: ['https://e.com/*'] })
})

test('activeTabId returns the id or null', async () => {
  const tabs = { query: vi.fn(async () => [{ id: 7 }]) }
  vi.stubGlobal('chrome', { tabs })
  expect(await activeTabId()).toBe(7)
  tabs.query.mockResolvedValueOnce([])
  expect(await activeTabId()).toBeNull()
})
