// ConfigService keeps GET /v1/config cached in kv.config, refreshes it daily (T28) and keeps
// the sync alarm on its sync_interval_minutes.
import type { RemoteConfig } from '../shared/config'
import type { Log } from '../shared/log'
import type { ApiClient } from './api'
import type { Db } from './db'

export const CONFIG_MAX_AGE_MS = 24 * 60 * 60 * 1000
export const SYNC_ALARM = 'sync'

async function scheduleSync(minutes: number): Promise<void> {
  await chrome.alarms.create(SYNC_ALARM, { periodInMinutes: minutes, delayInMinutes: minutes })
}

export interface ConfigDeps {
  db: Db
  api: Pick<ApiClient, 'config'>
  now: () => Date
  log: Log
}

export class ConfigService {
  constructor(private readonly deps: ConfigDeps) {}

  // current returns the cached config, fetching it only when there is none.
  async current(): Promise<RemoteConfig | null> {
    const cached = await this.deps.db.getKv('config')
    return cached ? cached.value : this.refresh()
  }

  // refresh fetches the config; on failure it keeps whatever was cached.
  async refresh(): Promise<RemoteConfig | null> {
    const { db, api, now, log } = this.deps
    try {
      const value = await api.config()
      await db.setKv('config', { fetchedAt: now().toISOString(), value })
      await scheduleSync(value.tracking.syncIntervalMinutes)
      return value
    } catch (e) {
      log.warn(`config: refresh failed (${e instanceof Error ? e.message : String(e)})`)
      return (await db.getKv('config'))?.value ?? null
    }
  }

  async refreshIfStale(): Promise<RemoteConfig | null> {
    const cached = await this.deps.db.getKv('config')
    if (cached && this.deps.now().getTime() - Date.parse(cached.fetchedAt) < CONFIG_MAX_AGE_MS) return cached.value
    return this.refresh()
  }

  // ensureSchedule (re)creates the sync alarm from the cached config, if any.
  async ensureSchedule(): Promise<void> {
    const cached = await this.deps.db.getKv('config')
    if (cached) await scheduleSync(cached.value.tracking.syncIntervalMinutes)
  }
}
