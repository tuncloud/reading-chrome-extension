// ConfigService keeps GET /v1/config cached in kv.config, refreshes it daily (T28) and keeps
// the sync alarm on its sync_interval_minutes.
import type { RemoteConfig } from '../domain/config'
import type { Api, Clock, LocalStore, Log, Scheduler } from './ports'

export const CONFIG_MAX_AGE_MS = 24 * 60 * 60 * 1000
export const SYNC_ALARM = 'sync'

export interface ConfigDeps {
  store: LocalStore
  api: Api
  clock: Clock
  scheduler: Scheduler
  log: Log
}

export class ConfigService {
  constructor(private readonly deps: ConfigDeps) {}

  // current returns the cached config, fetching it only when there is none.
  async current(): Promise<RemoteConfig | null> {
    const cached = await this.deps.store.getKv('config')
    return cached ? cached.value : this.refresh()
  }

  // refresh fetches the config; on failure it keeps whatever was cached.
  async refresh(): Promise<RemoteConfig | null> {
    const { store, api, clock, scheduler, log } = this.deps
    try {
      const value = await api.config()
      await store.setKv('config', { fetchedAt: clock.now().toISOString(), value })
      await scheduler.every(SYNC_ALARM, value.tracking.syncIntervalMinutes)
      return value
    } catch (e) {
      log.warn(`config: refresh failed (${e instanceof Error ? e.message : String(e)})`)
      return (await store.getKv('config'))?.value ?? null
    }
  }

  async refreshIfStale(): Promise<RemoteConfig | null> {
    const cached = await this.deps.store.getKv('config')
    if (cached && this.deps.clock.now().getTime() - Date.parse(cached.fetchedAt) < CONFIG_MAX_AGE_MS) return cached.value
    return this.refresh()
  }

  // ensureSchedule (re)creates the sync alarm from the cached config, if any.
  async ensureSchedule(): Promise<void> {
    const cached = await this.deps.store.getKv('config')
    if (cached) await this.deps.scheduler.every(SYNC_ALARM, cached.value.tracking.syncIntervalMinutes)
  }
}
