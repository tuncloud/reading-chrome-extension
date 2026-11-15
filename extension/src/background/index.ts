// Background service worker composition root (spec T28): the only place that syncs.
import { serveBackground } from '../shared/messaging'
import { SYNC_ALARM } from './config'
import { createRouter } from './router'
import { createServices } from './services'

const DAILY_ALARM = 'daily'
const DAY_MINUTES = 24 * 60

const s = createServices('background')

serveBackground(createRouter(s), s.log)

async function startup(): Promise<void> {
  await s.config.refreshIfStale()
  await s.config.ensureSchedule()
  await chrome.alarms.create(DAILY_ALARM, { periodInMinutes: DAY_MINUTES, delayInMinutes: DAY_MINUTES })
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

// On an update, also restore history once (an already-connected extension never hits the
// restore-after-connect path). restore.run() does nothing without a device token and never throws;
// it merges by larger-of, so repeating it is safe.
async function afterUpdate(): Promise<void> {
  await startup()
  await s.restore.run()
  await s.sync.run()
}

chrome.runtime.onInstalled.addListener((details?: chrome.runtime.InstalledDetails) =>
  details?.reason === 'update' ? run(afterUpdate, 'update') : run(startup, 'startup'),
)
chrome.runtime.onStartup.addListener(() => run(startup, 'startup'))

chrome.alarms.onAlarm.addListener((alarm) => {
  if (alarm.name === SYNC_ALARM) run(() => s.sync.run(), 'sync')
  else if (alarm.name === DAILY_ALARM) {
    run(async () => {
      await s.config.refreshIfStale()
      await s.sync.prune()
      await s.session.refresh()
    }, 'daily')
  }
})
