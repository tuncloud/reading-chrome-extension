// The popup's view model (spec T30), built from local data plus the server's article status.
import { roundMinutes, todaySeconds, type LocalArticle } from '../shared/reading'
import type { ApiClient } from '../background/api'
import type { Db } from '../background/db'
import type { ArticleStatus, SessionState } from '../shared/types'

export interface ArticleView {
  url: string
  title: string
  minutes: number
  progressPercent: number
  completed: boolean | null
  notionUrl: string | null
}

export type PopupModel =
  | { kind: 'disconnected' }
  | { kind: 'needs_setup'; workspaceName: string; setupError: string }
  | { kind: 'revoked'; workspaceName: string }
  | {
      kind: 'active'
      workspaceName: string
      todayMinutes: number
      current: ArticleView | null
      recent: ArticleView[]
      libraryUrl: string | null
      unsynced: number
    }

export interface PopupDeps {
  db: Db
  api: Pick<ApiClient, 'articlesStatus'>
  clock: { now(): Date; timeZone(): string }
}

const RECENT = 5
// Look back far enough to cover "today" in any time zone.
const TODAY_WINDOW_MS = 36 * 60 * 60 * 1000

function view(url: string, local: LocalArticle | undefined, server: ArticleStatus | undefined): ArticleView {
  const seconds = Math.max(local?.totalSeconds ?? 0, server?.totalSeconds ?? 0)
  const progress = Math.max(local?.maxProgress ?? 0, server?.maxProgress ?? 0)
  return {
    url,
    title: local?.title || url,
    minutes: roundMinutes(seconds),
    progressPercent: Math.round(progress * 100),
    completed: server ? server.completed : null,
    notionUrl: server?.notionUrl ?? null,
  }
}

export async function buildPopupModel(deps: PopupDeps, session: SessionState, currentUrl: string | null): Promise<PopupModel> {
  if (session.kind === 'disconnected' || !session.installation) return { kind: 'disconnected' }
  const inst = session.installation
  if (inst.status === 'revoked') return { kind: 'revoked', workspaceName: inst.workspaceName }
  if (inst.status !== 'active') return { kind: 'needs_setup', workspaceName: inst.workspaceName, setupError: inst.setupError }

  const { db, api, clock } = deps
  const now = clock.now()
  const todays = await db.segmentsSince(new Date(now.getTime() - TODAY_WINDOW_MS).toISOString())
  const recent = await db.recentArticles(RECENT)
  const current = currentUrl ? (await db.articles([currentUrl]))[0] : undefined
  const urls = [...new Set([...(currentUrl ? [currentUrl] : []), ...recent.map((a) => a.canonicalUrl)])]

  let status: Record<string, ArticleStatus> = {}
  const token = await db.getKv('device_token')
  if (token && urls.length > 0) {
    try {
      status = await api.articlesStatus(token, urls)
    } catch {
      // offline or failing backend: local values only
    }
  }

  const currentView = currentUrl && (current || status[currentUrl]) ? view(currentUrl, current, status[currentUrl]) : null
  return {
    kind: 'active',
    workspaceName: inst.workspaceName,
    todayMinutes: roundMinutes(todaySeconds(todays, now, clock.timeZone())),
    current: currentView,
    recent: recent.map((a) => view(a.canonicalUrl, a, status[a.canonicalUrl])),
    libraryUrl: inst.libraryUrl,
    unsynced: await db.countUnsynced(),
  }
}
