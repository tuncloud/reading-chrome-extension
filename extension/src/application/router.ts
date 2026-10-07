// createRouter maps background messages to use cases; the entry point wires it to chrome.runtime.
import type { ConfigService } from './config-service'
import type { DomainService } from './domains'
import { isBackgroundRequest } from './protocol'
import type { RecordService } from './record'
import type { SessionService } from './session'
import type { SyncService } from './sync'

export interface RouterServices {
  config: Pick<ConfigService, 'current'>
  domains: Pick<DomainService, 'plan'>
  recorder: Pick<RecordService, 'record'>
  session: Pick<SessionService, 'connect'>
  sync: Pick<SyncService, 'run'>
}

export function createRouter(s: RouterServices): (msg: unknown) => Promise<unknown> {
  return async (msg) => {
    if (!isBackgroundRequest(msg)) throw new Error('unknown message')
    switch (msg.type) {
      case 'tracking-plan': {
        const config = await s.config.current()
        if (!config) return null
        const adapter = await s.domains.plan(msg.host, config)
        return adapter ? { adapter, config } : null
      }
      case 'record': {
        // Tabs opened before a domain was denied or removed keep their tracker; the background decides.
        const config = await s.config.current()
        if (!config || !(await s.domains.plan(msg.host, config))) return { stored: false }
        return { stored: await s.recorder.record(msg.article, msg.segment) }
      }
      case 'connect':
        return s.session.connect()
      case 'sync-now':
        return s.sync.run()
    }
  }
}
