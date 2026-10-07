import { describe, expect, test, vi } from 'vitest'
import { parseRemoteConfig, type RemoteConfig } from '../domain/config'
import { wireConfig } from '../../test/fixtures'
import { META } from '../../test/store-contract'
import { createRouter, type RouterServices } from './router'

const cfg = parseRemoteConfig(wireConfig)

function services(config: RemoteConfig | null = cfg): RouterServices {
  return {
    config: { current: vi.fn(async () => config) },
    domains: { plan: vi.fn(async (host: string) => (host === 'medium.com' ? 'freedium-medium' as const : null)) },
    recorder: { record: vi.fn(async () => true) },
    session: { connect: vi.fn(async () => ({ ok: false as const, reason: 'cancelled' })) },
    sync: { run: vi.fn(async () => 'synced' as const) },
  }
}

describe('router', () => {
  test('tracking-plan returns the adapter with the config', async () => {
    const route = createRouter(services())
    expect(await route({ type: 'tracking-plan', host: 'medium.com' })).toEqual({ adapter: 'freedium-medium', config: cfg })
    expect(await route({ type: 'tracking-plan', host: 'example.com' })).toBeNull()
  })

  test('without config nothing is tracked (no inlined thresholds)', async () => {
    expect(await createRouter(services(null))({ type: 'tracking-plan', host: 'medium.com' })).toBeNull()
  })

  test('record, connect and sync-now delegate', async () => {
    const s = services()
    const route = createRouter(s)
    const segment = { id: 's1', sessionId: 'x', seq: 0, canonicalUrl: META.canonicalUrl, startedAt: '', endedAt: '', activeSeconds: 5, maxProgress: 0 }
    expect(await route({ type: 'record', host: 'medium.com', article: META, segment })).toEqual({ stored: true })
    expect(s.recorder.record).toHaveBeenCalledWith(META, segment)
    expect(await route({ type: 'connect' })).toEqual({ ok: false, reason: 'cancelled' })
    expect(await route({ type: 'sync-now' })).toBe('synced')
  })

  test('I3: a record from a host that is no longer tracked is not stored', async () => {
    const s = services()
    const route = createRouter(s)
    const segment = { id: 's1', sessionId: 'x', seq: 0, canonicalUrl: META.canonicalUrl, startedAt: '', endedAt: '', activeSeconds: 5, maxProgress: 0 }
    expect(await route({ type: 'record', host: 'denied.example.com', article: META, segment })).toEqual({ stored: false })
    expect(s.recorder.record).not.toHaveBeenCalled()
    expect(await createRouter(services(null))({ type: 'record', host: 'medium.com', article: META, segment })).toEqual({ stored: false })
  })

  test('unknown messages are rejected', async () => {
    const route = createRouter(services())
    await expect(route({ type: 'nope' })).rejects.toThrow(/unknown message/)
    await expect(route('hello')).rejects.toThrow(/unknown message/)
  })
})
