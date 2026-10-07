// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { installChrome, type ChromeMock } from '../../../test/chrome'
import { apiStub, captureLog, INSTALLATION, testDb } from '../../../test/helpers'
import { ApiError } from '../../shared/errors'
import { DomainService } from '../../background/domains'
import { SessionService } from '../../background/session'
import type { Db } from '../../background/db'
import { SetupService } from '../../background/setup'
import type { ConnectResult } from '../../background/session'
import { createOptionsController } from './options-controller'
import type { OptionsState } from './OptionsView'

describe('options controller', () => {
  let store: Db
  let api: ReturnType<typeof apiStub>
  let ch: ChromeMock
  let confirmAnswer: boolean
  let rendered: OptionsState[]
  let connect: () => Promise<ConnectResult>

  beforeEach(async () => {
    store = testDb()
    ch = installChrome()
    await store.setKv('device_token', 'rt_x')
    await store.setKv('installation', { ...INSTALLATION, status: 'needs_setup', libraryUrl: null })
    api = apiStub()
    confirmAnswer = false
    rendered = []
    connect = async () => ({ ok: false, reason: 'cancelled' })
  })

  afterEach(() => vi.unstubAllGlobals())

  afterEach(() => vi.unstubAllGlobals())

  function controller() {
    const session = new SessionService({ db: store, api, log: captureLog() })
    return createOptionsController(
      {
        session,
        setup: new SetupService({ db: store, api }),
        domains: new DomainService({ db: store }),
        connect: () => connect(),
      },
      { render: (s) => rendered.push(structuredClone(s)), confirm: () => confirmAnswer },
    )
  }

  test('load renders the stored session and domain lists', async () => {
    await store.setKv('enabled_domains', ['example.com'])
    const c = controller()
    await c.load()
    expect(rendered.at(-1)).toMatchObject({ domains: { enabled: ['example.com'], denied: [] }, busy: false })
    expect(rendered.at(-1)?.session).toMatchObject({ kind: 'connected' })
  })

  test('T31: enabling a domain prompts first and lists it', async () => {
    const c = controller()
    await c.load()
    const p = c.actions.enableDomain('example.com') // not awaited: the prompt must already be requested
    expect(ch.events[0]).toBe('request https://example.com/* https://*.example.com/*')
    await p
    expect(c.state()).toMatchObject({ domains: { enabled: ['example.com'] }, note: 'Tracking example.com.' })
  })

  test('M3: a Medium host is explained as always tracked', async () => {
    const c = controller()
    await c.load()
    await c.actions.enableDomain('medium.com')
    expect(c.state().note).toBe('Medium and Freedium are always tracked.')
  })

  test('an invalid domain is explained', async () => {
    const c = controller()
    await c.load()
    await c.actions.enableDomain('nope')
    expect(c.state().note).toBe('Enter a domain like example.com.')
  })

  test('T31: delete account needs confirmation', async () => {
    const c = controller()
    await c.load()
    await c.actions.deleteAccount()
    expect(api.count('deleteAccount')).toBe(0)
    confirmAnswer = true
    await c.actions.deleteAccount()
    expect(api.count('deleteAccount')).toBe(1)
    expect(c.state()).toMatchObject({ session: { kind: 'disconnected' }, note: 'Account deleted.' })
  })

  test('a failed delete keeps the session and says so', async () => {
    api = apiStub({ deleteAccount: async () => { throw new ApiError(500, 'internal', 'boom') } })
    confirmAnswer = true
    const c = controller()
    await c.load()
    await c.actions.deleteAccount()
    expect(c.state()).toMatchObject({ session: { kind: 'connected' }, note: 'Could not delete the account. Try again.' })
  })

  test('T31: setup search then choose activates the installation', async () => {
    api = apiStub({ setupPages: async () => [{ id: 'p1', title: 'Reading', url: 'https://www.notion.so/p1' }] })
    const c = controller()
    await c.load()
    await c.actions.searchPages('Read')
    expect(c.state().pages).toEqual([{ id: 'p1', title: 'Reading', url: 'https://www.notion.so/p1' }])
    await c.actions.choosePage('p1')
    expect(c.state()).toMatchObject({ pages: null, note: 'Reading Library and Daily Log created.' })
    expect(c.state().session).toMatchObject({ installation: { status: 'active' } })
  })

  test('a setup conflict shows the server message', async () => {
    api = apiStub({ setup: async () => { throw new ApiError(409, 'conflict', 'setup failed: property_missing:Progress') } })
    const c = controller()
    await c.load()
    await c.actions.choosePage('p1')
    expect(c.state().note).toBe('setup failed: property_missing:Progress')
  })

  test('disconnect logs out; connect failure shows the reason', async () => {
    const c = controller()
    await c.load()
    await c.actions.connect()
    expect(c.state().note).toBe('Connection cancelled.')
    await c.actions.disconnect()
    expect(api.count('logout')).toBe(1)
    expect(c.state()).toMatchObject({ session: { kind: 'disconnected' }, note: 'Disconnected.' })
  })

  test('busy is shown while an action runs', async () => {
    const c = controller()
    await c.load()
    rendered = []
    await c.actions.denyDomain('ads.com')
    expect(rendered[0]?.busy).toBe(true)
    expect(rendered.at(-1)).toMatchObject({ busy: false, domains: { denied: ['ads.com'] } })
  })

  test('render receives the actions object', async () => {
    const render = vi.fn()
    const c = createOptionsController(
      { session: { state: async () => ({ kind: 'disconnected' as const }) } as never, setup: {} as never,
        domains: { lists: async () => ({ enabled: [], denied: [] }) } as never, connect },
      { render, confirm: () => false },
    )
    await c.load()
    expect(render).toHaveBeenLastCalledWith(expect.anything(), c.actions)
  })
})
