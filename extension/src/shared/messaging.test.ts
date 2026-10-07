import { afterEach, describe, expect, test, vi } from 'vitest'
import { ExtensionGoneError, sendToBackground, serveBackground, serveTabRequests } from './messaging'

type Listener = (msg: unknown, sender: unknown, reply: (r: unknown) => void) => boolean | undefined

function fakeChrome(over: { id?: string; sendMessage?: (m: unknown) => Promise<unknown> } = {}) {
  const listeners: Listener[] = []
  const chrome = {
    runtime: {
      id: 'id' in over ? over.id : 'ext-id',
      sendMessage: vi.fn(over.sendMessage ?? (async () => ({ ok: true, value: 'pong' }))),
      onMessage: { addListener: (l: Listener) => listeners.push(l) },
    },
  }
  vi.stubGlobal('chrome', chrome)
  return { chrome, listeners }
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('sendToBackground', () => {
  test('unwraps the reply envelope', async () => {
    fakeChrome()
    expect(await sendToBackground({ type: 'sync-now' })).toBe('pong')
  })

  test('an error reply rejects with its message', async () => {
    fakeChrome({ sendMessage: async () => ({ ok: false, error: 'boom' }) })
    await expect(sendToBackground({ type: 'sync-now' })).rejects.toThrow('boom')
  })

  test('an invalidated context rejects with ExtensionGoneError', async () => {
    fakeChrome({ id: undefined })
    await expect(sendToBackground({ type: 'sync-now' })).rejects.toBeInstanceOf(ExtensionGoneError)
    fakeChrome({ sendMessage: async () => { throw new Error('Extension context invalidated.') } })
    await expect(sendToBackground({ type: 'sync-now' })).rejects.toBeInstanceOf(ExtensionGoneError)
  })
})

describe('serveBackground', () => {
  test('replies with the handler value, or the error, and keeps the channel open', async () => {
    const { listeners } = fakeChrome()
    const warn = vi.fn()
    serveBackground(async (m) => {
      if ((m as { type: string }).type === 'bad') throw new Error('nope')
      return 42
    }, { info: () => {}, warn })
    const replies: unknown[] = []
    expect(listeners[0]!({ type: 'ok' }, {}, (r) => replies.push(r))).toBe(true)
    listeners[0]!({ type: 'bad' }, {}, (r) => replies.push(r))
    await vi.waitFor(() => expect(replies).toHaveLength(2))
    expect(replies).toEqual([{ ok: true, value: 42 }, { ok: false, error: 'nope' }])
    expect(warn).toHaveBeenCalled()
  })
})

test('serveTabRequests answers current-page only', () => {
  const { listeners } = fakeChrome()
  serveTabRequests(() => ({ canonicalUrl: 'https://e.com/a' }))
  const replies: unknown[] = []
  expect(listeners[0]!({ type: 'current-page' }, {}, (r) => replies.push(r))).toBe(false)
  expect(listeners[0]!({ type: 'other' }, {}, (r) => replies.push(r))).toBe(false)
  expect(replies).toEqual([{ canonicalUrl: 'https://e.com/a' }])
})
