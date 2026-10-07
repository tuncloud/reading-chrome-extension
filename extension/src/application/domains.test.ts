import { beforeEach, describe, expect, test } from 'vitest'
import { FakeContentScripts, FakePermissions, MemoryStore } from '../../test/fakes'
import { parseRemoteConfig } from '../shared/config'
import { wireConfig } from '../../test/fixtures'
import type { KvKey, KvSchema } from './ports'
import { DomainService, SCRIPT_PREFIX } from './domains'

// EventStore logs storage access so tests can check what happened before the permission prompt.
class EventStore extends MemoryStore {
  constructor(readonly events: string[]) {
    super()
  }
  override async getKv<K extends KvKey>(key: K): Promise<KvSchema[K] | undefined> {
    this.events.push(`get ${key}`)
    return super.getKv(key)
  }
}

describe('DomainService', () => {
  let events: string[]
  let store: EventStore
  let permissions: FakePermissions
  let scripts: FakeContentScripts
  let svc: DomainService

  beforeEach(() => {
    events = []
    store = new EventStore(events)
    permissions = new FakePermissions(true, events)
    scripts = new FakeContentScripts()
    svc = new DomainService({ store, permissions, scripts })
  })

  test('T31: enabling asks for permission first, then registers the content script', async () => {
    expect(await svc.enable('https://www.Example.com/post')).toEqual({ ok: true, domain: 'example.com' })
    expect(events[0]).toBe('request https://example.com/* https://*.example.com/*')
    expect(scripts.scripts.get(`${SCRIPT_PREFIX}example.com`)).toEqual(['https://example.com/*', 'https://*.example.com/*'])
    expect(await store.getKv('enabled_domains')).toEqual(['example.com'])
  })

  test('invalid input never prompts', async () => {
    expect(await svc.enable('not a domain')).toEqual({ ok: false, reason: 'invalid' })
    expect(events).toEqual([])
  })

  test('a declined prompt registers and saves nothing', async () => {
    permissions.allow = false
    expect(await svc.enable('example.com')).toEqual({ ok: false, reason: 'permission_denied' })
    expect(scripts.scripts.size).toBe(0)
    expect(await store.getKv('enabled_domains')).toBeUndefined()
  })

  test('enabling twice keeps one entry', async () => {
    await svc.enable('example.com')
    expect(await svc.enable('example.com')).toEqual({ ok: false, reason: 'already_enabled' })
    expect(await store.getKv('enabled_domains')).toEqual(['example.com'])
  })

  test('I4: the permission prompt is requested synchronously, inside the user gesture', async () => {
    const p = svc.enable('example.com') // deliberately not awaited
    expect(events[0]).toBe('request https://example.com/* https://*.example.com/*')
    await p
  })

  test('M3: Medium and Freedium hosts are always tracked, so enabling them is refused before any prompt', async () => {
    const p = svc.enable('medium.com')
    expect(events).toEqual([])
    expect(await p).toEqual({ ok: false, reason: 'already_tracked' })
    expect(await svc.enable('www.medium.com')).toEqual({ ok: false, reason: 'already_tracked' })
    expect(events).toEqual([])
    expect(await store.getKv('enabled_domains')).toBeUndefined()
  })

  test('M3: disabling removes the list entry even when removing the permission fails', async () => {
    await svc.enable('example.com')
    permissions.remove = async () => { throw new Error('boom') }
    await svc.disable('example.com')
    expect(await store.getKv('enabled_domains')).toEqual([])
    expect(scripts.scripts.size).toBe(0)
  })

  test('T31: disabling unregisters the script and drops the permission', async () => {
    await svc.enable('example.com')
    await svc.enable('other.org')
    await svc.disable('example.com')
    expect([...scripts.scripts.keys()]).toEqual([`${SCRIPT_PREFIX}other.org`])
    expect(permissions.granted.has('https://example.com/*')).toBe(false)
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
    await scripts.register(`${SCRIPT_PREFIX}gone.org`, ['https://gone.org/*'])
    await scripts.register('something-else', ['https://x.org/*'])
    await svc.reconcile()
    expect([...scripts.scripts.keys()].sort()).toEqual([`${SCRIPT_PREFIX}example.com`, 'something-else'])
  })
})
