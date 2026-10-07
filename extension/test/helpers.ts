// Shared test helpers: a fresh IndexedDB, a fixed clock, a log capture and an API stub.
import { parseRemoteConfig } from '../src/shared/config'
import { mergeArticle, type ArticleMeta, type Segment, type StoredSegment } from '../src/shared/reading'
import type { Installation } from '../src/shared/types'
import type { Log } from '../src/shared/log'
import { Db, ReadingDb } from '../src/background/db'
import type { ApiClient } from '../src/background/api'
import { wireConfig } from './fixtures'

export function testDb(): Db {
  return new Db(new ReadingDb(`test-${crypto.randomUUID()}`))
}

export function clock(start = '2026-10-06T03:00:00.000Z', zone = 'Asia/Ho_Chi_Minh') {
  const c = {
    current: new Date(start),
    now: () => new Date(c.current),
    timeZone: () => zone,
    advance: (ms: number) => {
      c.current = new Date(c.current.getTime() + ms)
    },
  }
  return c
}

export function captureLog(): Log & { lines: string[] } {
  const lines: string[] = []
  return { lines, info: (m: string) => lines.push(`info ${m}`), warn: (m: string) => lines.push(`warn ${m}`) }
}

export const INSTALLATION: Installation = {
  id: 'b1', workspaceName: "Tuan's Notion", status: 'active', setupError: '', libraryUrl: 'https://www.notion.so/lib',
}

export const META: ArticleMeta = {
  canonicalUrl: 'https://medium.com/p/8f3c2a1b9d4e', source: 'medium', title: 'Designing Idempotent APIs',
  author: 'Jane Doe', siteName: 'Better Programming', wordCount: 1820, publishedAt: '2026-09-30',
  originalUrl: 'https://freedium-mirror.cfd/https://medium.com/x-8f3c2a1b9d4e',
}

export function segment(id: string, over: Partial<StoredSegment> = {}): StoredSegment {
  return {
    id, sessionId: 'sess', seq: 0, canonicalUrl: META.canonicalUrl,
    startedAt: '2026-10-06T03:00:00.000Z', endedAt: '2026-10-06T03:01:00.000Z', activeSeconds: 60, maxProgress: 0.5,
    synced: 0, createdAt: '2026-10-06T03:01:00.000Z', ...over,
  }
}

export async function record(db: Db, seg: StoredSegment, meta: ArticleMeta = META): Promise<boolean> {
  const plain: Segment = { ...seg }
  return db.applyRead(seg, (prev) => mergeArticle(prev, { ...meta, canonicalUrl: seg.canonicalUrl }, plain))
}

type Calls = { method: keyof ApiClient; args: unknown[] }[]

// apiStub answers like a healthy backend unless a method is overridden; calls are recorded.
export function apiStub(over: Partial<ApiClient> = {}): ApiClient & { calls: Calls; count(m: keyof ApiClient): number } {
  const calls: Calls = []
  const rec = <K extends keyof ApiClient>(m: K, args: unknown[]) => {
    calls.push({ method: m, args })
  }
  return {
    calls,
    count: (m) => calls.filter((c) => c.method === m).length,
    oauthStartUrl: (p) => {
      rec('oauthStartUrl', [p])
      return over.oauthStartUrl?.(p) ?? `https://api.example.com/v1/oauth/notion/start?code_challenge=${p.codeChallenge}`
    },
    config: async () => {
      rec('config', [])
      return over.config ? over.config() : parseRemoteConfig(wireConfig)
    },
    exchangeCode: async (p) => {
      rec('exchangeCode', [p])
      return over.exchangeCode
        ? over.exchangeCode(p)
        : { deviceToken: 'rt_new', deviceId: 'd1', user: { id: 'u1', name: 'Tuan Do' }, installation: INSTALLATION }
    },
    me: async (t) => {
      rec('me', [t])
      return over.me ? over.me(t) : { user: { id: 'u1', name: 'Tuan Do' }, device: { id: 'd1', name: 'Mac' }, installation: INSTALLATION }
    },
    logout: async (t) => {
      rec('logout', [t])
      if (over.logout) await over.logout(t)
    },
    deleteAccount: async (t) => {
      rec('deleteAccount', [t])
      if (over.deleteAccount) await over.deleteAccount(t)
    },
    push: async (t, body) => {
      rec('push', [t, body])
      return over.push ? over.push(t, body) : { accepted: body.segments.map((s) => s.id), duplicate: [], retry: [], rejected: [] }
    },
    articlesStatus: async (t, urls) => {
      rec('articlesStatus', [t, urls])
      return over.articlesStatus ? over.articlesStatus(t, urls) : {}
    },
    setupPages: async (t, q) => {
      rec('setupPages', [t, q])
      return over.setupPages ? over.setupPages(t, q) : []
    },
    setup: async (t, id) => {
      rec('setup', [t, id])
      return over.setup ? over.setup(t, id) : INSTALLATION
    },
  }
}
