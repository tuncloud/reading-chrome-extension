// A minimal chrome.* mock for tests. installChrome() stubs the global and returns handles.
import { vi } from 'vitest'

type Listener = (...args: any[]) => unknown

export interface ChromeMock {
  events: string[]
  alarms: Map<string, { periodInMinutes: number; delayInMinutes: number }>
  scripts: Map<string, string[]>
  granted: Set<string>
  allowPermission: boolean
  redirect: (url: string) => Promise<string | undefined>
  listeners: Record<string, Listener[]>
  chrome: any
}

export function installChrome(): ChromeMock {
  const m: ChromeMock = {
    events: [], alarms: new Map(), scripts: new Map(), granted: new Set(), allowPermission: true,
    redirect: async () => 'https://ext.chromiumapp.org/?code=one-time', listeners: {}, chrome: null,
  }
  const listen = (name: string) => ({ addListener: (l: Listener) => (m.listeners[name] ??= []).push(l) })
  m.chrome = {
    runtime: { id: 'ext-id', onMessage: listen('runtime.onMessage'), onInstalled: listen('runtime.onInstalled'), onStartup: listen('runtime.onStartup'),
      sendMessage: vi.fn(async () => ({ ok: true, value: null })), openOptionsPage: vi.fn(async () => {}) },
    alarms: {
      create: vi.fn(async (name: string, info: { periodInMinutes: number; delayInMinutes: number }) => { m.alarms.set(name, info) }),
      onAlarm: listen('alarms.onAlarm'),
    },
    identity: {
      getRedirectURL: () => 'https://ext.chromiumapp.org/',
      launchWebAuthFlow: vi.fn(async ({ url }: { url: string }) => {
        m.events.push(`launch ${url}`)
        return m.redirect(url)
      }),
    },
    permissions: {
      request: vi.fn(async ({ origins }: { origins: string[] }) => {
        m.events.push(`request ${origins.join(' ')}`)
        if (m.allowPermission) for (const o of origins) m.granted.add(o)
        return m.allowPermission
      }),
      remove: vi.fn(async ({ origins }: { origins: string[] }) => {
        m.events.push(`remove ${origins.join(' ')}`)
        for (const o of origins) m.granted.delete(o)
        return true
      }),
    },
    scripting: {
      registerContentScripts: vi.fn(async (list: { id: string; matches: string[] }[]) => { for (const s of list) m.scripts.set(s.id, s.matches) }),
      updateContentScripts: vi.fn(async (list: { id: string; matches: string[] }[]) => { for (const s of list) m.scripts.set(s.id, s.matches) }),
      unregisterContentScripts: vi.fn(async ({ ids }: { ids: string[] }) => { for (const id of ids) m.scripts.delete(id) }),
      getRegisteredContentScripts: vi.fn(async () => [...m.scripts.keys()].map((id) => ({ id }))),
    },
    tabs: { query: vi.fn(async () => [{ id: 7 }]), create: vi.fn(async () => ({})), sendMessage: vi.fn(async () => undefined) },
  }
  vi.stubGlobal('chrome', m.chrome)
  return m
}
