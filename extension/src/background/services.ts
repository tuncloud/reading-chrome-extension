// createServices builds the background services. The service worker, popup and options each
// call it; they share one IndexedDB. It registers no chrome listeners (only index.ts does).
import { consoleLog, type Log } from '../shared/log'
import { Api } from './api'
import { ConfigService } from './config'
import { Db, ReadingDb } from './db'
import { DomainService } from './domains'
import { RecordService } from './record'
import { SessionService } from './session'
import { SetupService } from './setup'
import { SyncService } from './sync'

export interface Services {
  db: Db
  api: Api
  log: Log
  config: ConfigService
  sync: SyncService
  recorder: RecordService
  session: SessionService
  setup: SetupService
  domains: DomainService
}

export const now = () => new Date()
export const timeZone = () => Intl.DateTimeFormat().resolvedOptions().timeZone

export function createServices(scope: string): Services {
  const db = new Db(new ReadingDb())
  const api = new Api({ baseUrl: import.meta.env.VITE_API_BASE as string })
  const log = consoleLog(scope)
  const config = new ConfigService({ db, api, now, log })
  const sync = new SyncService({ db, api, clock: { now, timeZone }, log })
  const syncSoon = () => {
    sync.run().catch((e: unknown) => log.warn(`sync failed: ${e instanceof Error ? e.message : String(e)}`))
  }
  return {
    db, api, log, config, sync,
    recorder: new RecordService({ db, now, config: () => config.current(), syncSoon, log }),
    session: new SessionService({ db, api, log, afterConnect: syncSoon }),
    setup: new SetupService({ db, api }),
    domains: new DomainService({ db }),
  }
}
