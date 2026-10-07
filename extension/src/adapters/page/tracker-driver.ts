// The DOM side of the tracker (spec T27): browser events and a 1 s tick in, segments out.
import type { TrackingConfig } from '../../domain/config'
import { readingProgress } from '../../domain/progress'
import type { ArticleMeta, Segment } from '../../domain/reading'
import { Tracker, type SegmentDraft } from '../../domain/tracker'

export interface DriverDeps {
  win: Window
  config: TrackingConfig
  // extract reads the article from the live page; null means "not an article".
  extract: () => ArticleMeta | null
  contentRoot: () => Element
  // send delivers one segment; a rejection means the extension is gone and tracking stops.
  send: (article: ArticleMeta, segment: Segment) => Promise<unknown>
  newId: () => string
  now?: () => number
}

export interface Driver {
  stop(): void
  current(): ArticleMeta | null
  readonly stopped: boolean
}

const TICK_MS = 1000
const MOUSEMOVE_EVERY_MS = 1000
const INPUT_EVENTS = ['wheel', 'keydown', 'pointerdown', 'touchstart'] as const
const CLAIM = Symbol.for('reading-tracker.claimed')

// claimPage returns true the first time it is called for a window (Review Focus 2).
export function claimPage(win: Window): boolean {
  const w = win as Window & { [CLAIM]?: boolean }
  if (w[CLAIM]) return false
  w[CLAIM] = true
  return true
}

// releasePage lets a later injection claim the page again (no tracking plan was obtained).
export function releasePage(win: Window): void {
  delete (win as Window & { [CLAIM]?: boolean })[CLAIM]
}

const withoutHash = (url: string) => url.split('#')[0]!

export function startTracking(d: DriverDeps): Driver {
  const { win } = d
  const doc = win.document
  const now = d.now ?? (() => Date.now())
  const tracker = new Tracker({
    idleSeconds: d.config.idleSeconds,
    segmentSeconds: d.config.segmentSeconds,
    now: now(),
    visible: doc.visibilityState === 'visible',
    focused: doc.hasFocus(),
    newId: d.newId,
  })
  let meta = d.extract()
  let href = withoutHash(win.location.href)
  let lastMove = Number.NEGATIVE_INFINITY
  let stopped = false

  const emit = (drafts: SegmentDraft[], article: ArticleMeta | null) => {
    if (!article) return
    for (const s of drafts) {
      const segment: Segment = {
        id: s.id, sessionId: s.sessionId, seq: s.seq, canonicalUrl: article.canonicalUrl,
        startedAt: new Date(s.startedAt).toISOString(), endedAt: new Date(s.endedAt).toISOString(),
        activeSeconds: s.activeSeconds, maxProgress: s.maxProgress,
      }
      d.send(article, segment).catch(() => stop())
    }
  }

  // flush re-reads metadata first, so late-rendered titles and word counts are captured.
  const flush = (drafts: SegmentDraft[]) => {
    if (drafts.length === 0) return
    meta = d.extract() ?? meta
    emit(drafts, meta)
  }

  const updateProgress = () => {
    const rect = d.contentRoot().getBoundingClientRect()
    tracker.progress(readingProgress({
      scrollY: win.scrollY, viewportHeight: win.innerHeight, rootTop: rect.top + win.scrollY, rootHeight: rect.height,
    }))
  }

  const onInput = () => {
    tracker.input(now())
  }
  const onScroll = () => {
    updateProgress()
    tracker.input(now())
  }
  const onMouseMove = () => {
    const t = now()
    if (t - lastMove >= MOUSEMOVE_EVERY_MS) {
      lastMove = t
      tracker.input(t)
    }
  }
  const onVisibility = () => flush(tracker.setVisible(doc.visibilityState === 'visible', now()))
  const onFocus = () => flush(tracker.setFocused(true, now()))
  const onBlur = () => flush(tracker.setFocused(false, now()))
  const onPageHide = () => {
    updateProgress()
    flush(tracker.pagehide(now()))
    stop()
  }

  const tick = () => {
    const current = withoutHash(win.location.href)
    if (current !== href) {
      href = current
      emit(tracker.newSession(now()), meta) // the open segment belongs to the previous article
      meta = d.extract()
    }
    updateProgress()
    flush(tracker.tick(now()))
  }

  const listen: [EventTarget, string, EventListener][] = [
    ...INPUT_EVENTS.map((type): [EventTarget, string, EventListener] => [win, type, onInput]),
    [win, 'scroll', onScroll],
    [win, 'mousemove', onMouseMove],
    [doc, 'visibilitychange', onVisibility],
    [win, 'focus', onFocus],
    [win, 'blur', onBlur],
    [win, 'pagehide', onPageHide],
  ]
  for (const [target, type, fn] of listen) target.addEventListener(type, fn, { passive: true, capture: true })
  const timer = win.setInterval(tick, TICK_MS)

  function stop() {
    if (stopped) return
    stopped = true
    win.clearInterval(timer)
    for (const [target, type, fn] of listen) target.removeEventListener(type, fn, { capture: true })
  }

  return {
    stop,
    current: () => meta,
    get stopped() {
      return stopped
    },
  }
}
