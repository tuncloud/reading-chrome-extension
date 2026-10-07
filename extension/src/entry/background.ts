// Background service worker composition root (spec T28): the only place that syncs.
import { ChromeScheduler, onAlarm } from '../adapters/chrome/alarms'
import { serveBackground } from '../shared/messaging'
import { SYNC_ALARM } from '../background/config'
import { createRouter } from '../application/router'
import { compose } from './compose'

const DAILY_ALARM = 'daily'
const DAY_MINUTES = 24 * 60

const s = compose('background')

serveBackground(createRouter(s), s.log)

async function startup(): Promise<void> {
  await s.config.refreshIfStale()
  await s.config.ensureSchedule()
  await new ChromeScheduler().every(DAILY_ALARM, DAY_MINUTES)
  try {
    await s.domains.reconcile()
  } catch (e: unknown) {
    s.log.warn(`reconcile failed: ${e instanceof Error ? e.message : String(e)}`)
  }
  await s.sync.prune()
  await s.session.refresh()
  await s.sync.run()
}

function run(task: () => Promise<unknown>, what: string): void {
  task().catch((e: unknown) => s.log.warn(`${what} failed: ${e instanceof Error ? e.message : String(e)}`))
}

chrome.runtime.onInstalled.addListener(() => run(startup, 'startup'))
chrome.runtime.onStartup.addListener(() => run(startup, 'startup'))

onAlarm((name) => {
  if (name === SYNC_ALARM) run(() => s.sync.run(), 'sync')
  else if (name === DAILY_ALARM) {
    run(async () => {
      await s.config.refreshIfStale()
      await s.sync.prune()
      await s.session.refresh()
    }, 'daily')
  }
})
