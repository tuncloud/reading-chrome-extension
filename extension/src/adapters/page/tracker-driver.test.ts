// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import type { TrackingConfig } from '../../domain/config'
import type { ArticleMeta, Segment } from '../../domain/reading'
import { claimPage, releasePage, startTracking, type Driver } from './tracker-driver'

const config: TrackingConfig = {
  idleSeconds: 60, segmentSeconds: 60, minCreateSeconds: 30, completeProgress: 0.9, completeTimeRatio: 0.3,
  wordsPerMinute: 230, syncIntervalMinutes: 2, syncThresholdSegments: 20,
}

function meta(url: string, title = 'Title'): ArticleMeta {
  return { canonicalUrl: url, source: 'web', title, author: '', siteName: '', wordCount: 100, publishedAt: '', originalUrl: url }
}

describe('startTracking', () => {
  let visibility: DocumentVisibilityState
  let sent: { article: ArticleMeta; segment: Segment }[]
  let send: (article: ArticleMeta, segment: Segment) => Promise<unknown>
  let driver: Driver | null
  let n: number

  beforeEach(() => {
    vi.useFakeTimers({ now: new Date('2026-10-06T03:00:00.000Z') })
    visibility = 'visible'
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => visibility })
    document.hasFocus = () => true
    history.replaceState(null, '', '/post/a')
    document.body.innerHTML = '<article>text</article>'
    sent = []
    send = async (article, segment) => { sent.push({ article, segment }) }
    driver = null
    n = 0
  })

  afterEach(() => {
    driver?.stop()
    vi.useRealTimers()
  })

  const start = (extract: () => ArticleMeta | null = () => meta(location.href)) => {
    driver = startTracking({
      win: window, config, extract, contentRoot: () => document.querySelector('article')!,
      send: (a, s) => send(a, s), newId: () => `00000000-0000-4000-8000-${String(++n).padStart(12, '0')}`,
    })
    return driver
  }

  const scrollEvery = (seconds: number, total: number) => {
    for (let t = 1; t <= total; t++) {
      if (t % seconds === 0) window.dispatchEvent(new Event('scroll'))
      vi.advanceTimersByTime(1000)
    }
  }

  test('T27: 150 s of reading → segments of 60, 60 and 30 on pagehide, with ISO times', () => {
    start()
    scrollEvery(10, 150)
    window.dispatchEvent(new Event('pagehide'))
    expect(sent.map((s) => s.segment.activeSeconds)).toEqual([60, 60, 30])
    const first = sent[0]!.segment
    expect(first).toMatchObject({ seq: 0, canonicalUrl: 'http://localhost:3000/post/a' })
    expect(first.startedAt).toBe('2026-10-06T03:00:00.000Z')
    expect(first.endedAt).toBe('2026-10-06T03:01:00.000Z')
    expect(driver!.stopped).toBe(true)
  })

  test('a hidden tab accrues nothing; leaving flushes what was read', () => {
    start()
    vi.advanceTimersByTime(10_000)
    visibility = 'hidden'
    document.dispatchEvent(new Event('visibilitychange'))
    expect(sent.map((s) => s.segment.activeSeconds)).toEqual([10])
    vi.advanceTimersByTime(120_000)
    window.dispatchEvent(new Event('pagehide'))
    expect(sent).toHaveLength(1)
  })

  test('window blur pauses; focus resumes', () => {
    start()
    vi.advanceTimersByTime(5_000)
    window.dispatchEvent(new Event('blur'))
    vi.advanceTimersByTime(60_000)
    window.dispatchEvent(new Event('focus'))
    vi.advanceTimersByTime(5_000)
    window.dispatchEvent(new Event('pagehide'))
    expect(sent.map((s) => s.segment.activeSeconds)).toEqual([5, 5])
  })

  test('C1: focus changes on an element inside the page do not pause tracking', () => {
    document.body.innerHTML = '<article>text <button id="b">clap</button></article>'
    start()
    vi.advanceTimersByTime(10_000)
    const b = document.getElementById('b')!
    b.dispatchEvent(new FocusEvent('focus'))
    b.dispatchEvent(new FocusEvent('blur'))
    scrollEvery(10, 50)
    window.dispatchEvent(new Event('pagehide'))
    expect(sent.reduce((sum, s) => sum + s.segment.activeSeconds, 0)).toBe(60)
  })

  test('mousemove keeps the reader active', () => {
    start()
    for (let t = 1; t <= 120; t++) {
      if (t % 30 === 0) window.dispatchEvent(new MouseEvent('mousemove'))
      vi.advanceTimersByTime(1000)
    }
    window.dispatchEvent(new Event('pagehide'))
    expect(sent.reduce((sum, s) => sum + s.segment.activeSeconds, 0)).toBe(120)
  })

  test('progress comes from the content root geometry', () => {
    const root = document.querySelector('article')!
    root.getBoundingClientRect = () => ({ top: -1400, height: 3000 }) as DOMRect
    Object.defineProperty(window, 'scrollY', { configurable: true, value: 1600 })
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 800 })
    start()
    vi.advanceTimersByTime(3_000)
    window.dispatchEvent(new Event('pagehide'))
    // rootTop = −1400 + 1600 = 200; (1600 + 800 − 200) / 3000
    expect(sent[0]!.segment.maxProgress).toBeCloseTo(0.7333, 3)
  })

  test('SPA navigation sends the open segment under the previous article and starts a new session', () => {
    start((): ArticleMeta => meta(location.href, location.pathname))
    vi.advanceTimersByTime(10_000)
    history.pushState(null, '', '/post/b')
    vi.advanceTimersByTime(1_000)
    expect(sent).toHaveLength(1)
    expect(sent[0]!.article.canonicalUrl).toBe('http://localhost:3000/post/a')
    expect(sent[0]!.segment.canonicalUrl).toBe('http://localhost:3000/post/a')
    vi.advanceTimersByTime(4_000)
    window.dispatchEvent(new Event('pagehide'))
    const second = sent[1]!
    expect(second.article.title).toBe('/post/b')
    expect(second.segment.seq).toBe(0)
    expect(second.segment.sessionId).not.toBe(sent[0]!.segment.sessionId)
    expect(driver!.current()?.canonicalUrl).toBe('http://localhost:3000/post/b')
  })

  test('a hash-only change keeps the reading session', () => {
    start(() => meta('http://localhost:3000/post/a'))
    vi.advanceTimersByTime(5_000)
    history.pushState(null, '', location.pathname + '#section-2')
    vi.advanceTimersByTime(5_000)
    const before = driver!.current()
    window.dispatchEvent(new Event('pagehide'))
    expect(sent.map((s) => s.segment.activeSeconds)).toEqual([10])
    expect(driver!.current()).toEqual(before)
  })

  test('metadata is re-read at flush time', () => {
    let title = ''
    start(() => meta(location.href, title))
    title = 'Rendered later'
    vi.advanceTimersByTime(3_000)
    window.dispatchEvent(new Event('pagehide'))
    expect(sent[0]!.article.title).toBe('Rendered later')
  })

  test('a page that is not an article sends nothing', () => {
    start(() => null)
    vi.advanceTimersByTime(90_000)
    window.dispatchEvent(new Event('pagehide'))
    expect(sent).toEqual([])
  })

  test('Review focus 1: stops after the extension context is invalidated', async () => {
    let calls = 0
    send = async () => {
      calls++
      throw new Error('Extension context invalidated.')
    }
    start()
    scrollEvery(10, 60)
    await vi.runOnlyPendingTimersAsync()
    expect(driver!.stopped).toBe(true)
    scrollEvery(10, 120)
    expect(calls).toBe(1)
  })

  test('after stop no event or timer does anything', () => {
    const d = start()
    d.stop()
    window.dispatchEvent(new Event('scroll'))
    vi.advanceTimersByTime(120_000)
    window.dispatchEvent(new Event('pagehide'))
    expect(sent).toEqual([])
  })
})

test('Review focus 2: a second injection does not start a second tracker', () => {
  const win = {} as Window
  expect(claimPage(win)).toBe(true)
  expect(claimPage(win)).toBe(false)
})

test('releasePage lets the page be claimed again', () => {
  const win = {} as Window
  expect(claimPage(win)).toBe(true)
  releasePage(win)
  expect(claimPage(win)).toBe(true)
})
