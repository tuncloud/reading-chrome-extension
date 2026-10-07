// compose builds every service from the real adapters. Background, popup and options each
// call it; they share one IndexedDB, so they see the same data.
import { ChromeAuthFlow } from '../adapters/chrome/identity'
import { ChromePermissions } from '../adapters/chrome/permissions'
import { ChromeContentScripts } from '../adapters/chrome/scripting'
import { deviceName } from '../background/device'
import { WebCryptoPkce } from '../background/pkce'
import { Api as HttpApi } from '../background/api'
import { Db, ReadingDb } from '../background/db'
import { consoleLog } from '../shared/log'
import { ConfigService } from '../background/config'
import { DomainService } from '../application/domains'
import type { Api, Clock, LocalStore, Log } from '../application/ports'
import { RecordService } from '../background/record'
import { SessionService } from '../application/session'
import { SetupService } from '../background/setup'
import { SyncService } from '../background/sync'

export interface Services {
  store: LocalStore
  api: Api
  clock: Clock
  log: Log
  config: ConfigService
  sync: SyncService
  recorder: RecordService
  session: SessionService
  setup: SetupService
  domains: DomainService
}

export function compose(scope: string): Services {
  const apiBase = import.meta.env.VITE_API_BASE as string
  const store = new Db(new ReadingDb())
  const api = new HttpApi({ baseUrl: apiBase })
  const clock = { now: () => new Date(), timeZone: () => Intl.DateTimeFormat().resolvedOptions().timeZone }
  const log = consoleLog(scope)
  const config = new ConfigService({ db: store, api, now: () => clock.now(), log })
  const sync = new SyncService({ db: store, api, clock, log })
  const syncSoon = () => {
    sync.run().catch((e: unknown) => log.warn(`sync failed: ${e instanceof Error ? e.message : String(e)}`))
  }
  return {
    store, api, clock, log, config, sync,
    recorder: new RecordService({ db: store, now: () => clock.now(), config: () => config.current(), syncSoon, log }),
    session: new SessionService({
      store, api, authFlow: new ChromeAuthFlow(), pkce: new WebCryptoPkce(), deviceName: () => deviceName(navigator), log, afterConnect: syncSoon,
    }),
    setup: new SetupService({ db: store, api }),
    domains: new DomainService({ store, permissions: new ChromePermissions(), scripts: new ChromeContentScripts() }),
  }
}
