// RestoreService pulls the server's reading history into the local store after a connect, so the
// popup is not empty on a reinstalled extension. It only adds to articles (never segments), so
// restored data is never pushed back, and it never throws: a failed restore must not block connecting.
import { localDate, restoreArticle, todayCarry } from '../shared/reading'
import type { Log } from '../shared/log'
import type { ApiClient } from './api'
import type { Db } from './db'

export const HISTORY_LIMIT = 50
// Look back far enough to cover a calendar day in any time zone.
const WINDOW_MS = 36 * 60 * 60 * 1000

export interface RestoreDeps {
  db: Db
  api: Pick<ApiClient, 'history'>
  clock: { now(): Date; timeZone(): string }
  log: Log
}

export class RestoreService {
  constructor(private readonly deps: RestoreDeps) {}

  // run resolves true when history was stored, false otherwise.
  async run(): Promise<boolean> {
    const { db, api, clock, log } = this.deps
    try {
      const token = await db.getKv('device_token')
      if (!token) return false
      const tz = clock.timeZone()
      const history = await api.history(token, { timeZone: tz, limit: HISTORY_LIMIT })
      const now = clock.now()
      const recent = await db.segmentsSince(new Date(now.getTime() - WINDOW_MS).toISOString())
      const local = recent
        .filter((s) => localDate(new Date(s.startedAt), tz) === history.today.date)
        .reduce((sum, s) => sum + s.activeSeconds, 0)
      await db.restoreArticles(history.articles, restoreArticle)
      await db.setKv('today_carry', { date: history.today.date, seconds: todayCarry(history.today.seconds, local) })
      return true
    } catch (e) {
      log.warn(`restore: history not restored (${e instanceof Error ? e.message : String(e)})`)
      return false
    }
  }
}
