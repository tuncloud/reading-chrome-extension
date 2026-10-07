// Content script composition root: ask the background whether and how to track this
// page, then run the tracker driver with the chosen site adapter.
import { ExtensionGoneError, sendToBackground, serveTabRequests } from '../shared/messaging'
import { claimPage, releasePage, startTracking, type Driver } from './driver'
import { adapterFor } from './sites/registry'
import type { ArticleMeta, Segment } from '../shared/reading'

async function main(): Promise<void> {
  if (!claimPage(window)) return
  const plan = await sendToBackground({ type: 'tracking-plan', host: location.hostname }).catch((e: unknown) => {
    if (!(e instanceof ExtensionGoneError)) console.warn('reading-tracker: no tracking plan', e)
    return null
  })
  if (!plan) {
    releasePage(window)
    return
  }
  const adapter = adapterFor(plan.adapter)
  let driver: Driver | null = null

  const send = (article: ArticleMeta, segment: Segment) =>
    sendToBackground({ type: 'record', host: location.hostname, article, segment }).then(
      () => undefined,
      (e: unknown) => {
        if (e instanceof ExtensionGoneError) throw e // stops the driver
        console.warn('reading-tracker: segment not recorded', e)
      },
    )

  const start = () => {
    driver = startTracking({
      win: window,
      config: plan.config.tracking,
      extract: () => adapter.extract({ doc: document, location: location.href, config: plan.config }),
      contentRoot: () => adapter.contentRoot(document),
      send,
      newId: () => crypto.randomUUID(),
    })
  }

  start()
  // A page restored from the back/forward cache gets a fresh tracker.
  window.addEventListener('pageshow', (e) => {
    if (e.persisted && driver?.stopped) start()
  })
  serveTabRequests(() => ({ canonicalUrl: driver?.current()?.canonicalUrl ?? null }))
}

void main()
