import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { installChrome, type ChromeMock } from '../../test/chrome'

const order: string[] = []
const stub = (name: string, impl: () => Promise<unknown> = async () => undefined) =>
  vi.fn(async () => {
    order.push(name)
    return impl()
  })
const svc = {
  config: { refreshIfStale: stub('config.refreshIfStale'), ensureSchedule: stub('config.ensureSchedule') },
  domains: { reconcile: stub('domains.reconcile') },
  sync: { prune: stub('sync.prune'), run: stub('sync.run') },
  session: { refresh: stub('session.refresh') },
  log: { info: vi.fn(), warn: vi.fn() },
}

vi.mock('./services', () => ({ createServices: () => svc }))

let ch: ChromeMock

beforeEach(async () => {
  vi.resetModules()
  order.length = 0
  svc.domains.reconcile.mockImplementation(async () => {
    order.push('domains.reconcile')
  })
  ch = installChrome()
  ch.chrome.alarms.create.mockImplementation(async (name: string, info: { periodInMinutes: number; delayInMinutes: number }) => {
    order.push(`alarms.create ${name} ${JSON.stringify(info)}`)
  })
  await import('./index')
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.clearAllMocks()
})

const flush = () => new Promise((r) => setTimeout(r, 0))

test('registers one listener each for install, startup, alarms and messages', () => {
  for (const name of ['runtime.onInstalled', 'runtime.onStartup', 'alarms.onAlarm', 'runtime.onMessage']) {
    expect(ch.listeners[name], name).toHaveLength(1)
  }
})

test('onInstalled runs the startup sequence in order', async () => {
  ch.listeners['runtime.onInstalled']![0]!()
  await flush()
  expect(order).toEqual([
    'config.refreshIfStale',
    'config.ensureSchedule',
    'alarms.create daily {"periodInMinutes":1440,"delayInMinutes":1440}',
    'domains.reconcile',
    'sync.prune',
    'session.refresh',
    'sync.run',
  ])
  expect(ch.chrome.alarms.create).toHaveBeenCalledWith('daily', { periodInMinutes: 1440, delayInMinutes: 1440 })
})

test('a failing reconcile does not stop startup', async () => {
  svc.domains.reconcile.mockRejectedValue(new Error('boom'))
  ch.listeners['runtime.onStartup']![0]!()
  await flush()
  expect(svc.sync.run).toHaveBeenCalled()
  expect(svc.log.warn).toHaveBeenCalledWith('reconcile failed: boom')
})

test('the sync alarm runs sync', async () => {
  ch.listeners['alarms.onAlarm']![0]!({ name: 'sync' })
  await flush()
  expect(svc.sync.run).toHaveBeenCalledTimes(1)
  expect(order).toEqual(['sync.run'])
})

test('the daily alarm refreshes config, prunes and refreshes the session', async () => {
  ch.listeners['alarms.onAlarm']![0]!({ name: 'daily' })
  await flush()
  expect(order).toEqual(['config.refreshIfStale', 'sync.prune', 'session.refresh'])
  expect(svc.sync.run).not.toHaveBeenCalled()
})
