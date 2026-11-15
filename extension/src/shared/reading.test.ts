import { describe, expect, test } from 'vitest'
import { localDate, mergeArticle, restoreArticle, roundMinutes, todayCarry, todaySeconds, type ArticleMeta, type LocalArticle, type Segment } from './reading'
import type { HistoryArticle } from './types'

const meta: ArticleMeta = {
  canonicalUrl: 'https://medium.com/p/8f3c2a1b9d4e', source: 'medium', title: 'Designing Idempotent APIs',
  author: 'Jane Doe', siteName: 'Better Programming', wordCount: 1820, publishedAt: '2026-09-30',
  originalUrl: 'https://freedium-mirror.cfd/https://medium.com/x-8f3c2a1b9d4e',
}

function seg(over: Partial<Segment> = {}): Segment {
  return {
    id: 's1', sessionId: 'sess', seq: 0, canonicalUrl: meta.canonicalUrl,
    startedAt: '2026-10-06T03:01:05.000Z', endedAt: '2026-10-06T03:02:10.000Z', activeSeconds: 58, maxProgress: 0.42, ...over,
  }
}

describe('mergeArticle', () => {
  test('first segment creates the article', () => {
    expect(mergeArticle(undefined, meta, seg())).toEqual({
      ...meta, totalSeconds: 58, maxProgress: 0.42, lastReadAt: '2026-10-06T03:02:10.000Z',
    })
  })

  test('later segments add seconds, keep max progress and latest read time', () => {
    const a = mergeArticle(undefined, meta, seg())
    const b = mergeArticle(a, meta, seg({ id: 's2', activeSeconds: 30, maxProgress: 0.3, endedAt: '2026-10-06T03:05:00.000Z' }))
    expect(b.totalSeconds).toBe(88)
    expect(b.maxProgress).toBe(0.42)
    expect(b.lastReadAt).toBe('2026-10-06T03:05:00.000Z')
    const c = mergeArticle(b, meta, seg({ id: 's0', endedAt: '2026-10-06T02:00:00.000Z' }))
    expect(c.lastReadAt).toBe('2026-10-06T03:05:00.000Z')
  })

  test('empty metadata does not erase what an earlier extraction found', () => {
    const a = mergeArticle(undefined, meta, seg())
    const b = mergeArticle(a, { ...meta, title: '', author: '  ', wordCount: 0 }, seg({ id: 's2' }))
    expect(b.title).toBe('Designing Idempotent APIs')
    expect(b.author).toBe('Jane Doe')
    expect(b.wordCount).toBe(1820)
  })
})

test('roundMinutes rounds half up like the backend', () => {
  expect(roundMinutes(89)).toBe(1)
  expect(roundMinutes(90)).toBe(2)
  expect(roundMinutes(0)).toBe(0)
})

test('localDate uses the given time zone (scenario 16)', () => {
  expect(localDate(new Date('2026-10-05T18:30:00Z'), 'Asia/Ho_Chi_Minh')).toBe('2026-10-06')
  expect(localDate(new Date('2026-10-05T18:30:00Z'), 'UTC')).toBe('2026-10-05')
})

test('todaySeconds counts only segments that started today in the local zone', () => {
  const now = new Date('2026-10-06T10:00:00Z') // 17:00 in Ho Chi Minh
  const segments = [
    seg({ startedAt: '2026-10-05T16:59:00Z', activeSeconds: 60 }), // 23:59 on the 5th, local
    seg({ startedAt: '2026-10-05T17:00:00Z', activeSeconds: 45 }), // 00:00 on the 6th, local
    seg({ startedAt: '2026-10-06T09:00:00Z', activeSeconds: 30 }),
  ]
  expect(todaySeconds(segments, now, 'Asia/Ho_Chi_Minh')).toBe(75)
})

const serverArticle = (over: Partial<HistoryArticle> = {}): HistoryArticle => ({
  canonicalUrl: meta.canonicalUrl, source: 'medium', title: 'S', author: 'A', siteName: 'Server Site', wordCount: 1000,
  publishedAt: '2026-09-30', totalSeconds: 180, maxProgress: 0.9, lastReadAt: '2026-10-06T12:00:00Z', completed: false,
  notionUrl: null, ...over,
})

describe('restoreArticle', () => {
  test('creates a local article from the server one', () => {
    const s = serverArticle()
    expect(restoreArticle(undefined, s)).toEqual({
      canonicalUrl: s.canonicalUrl, source: 'medium', title: 'S', author: 'A', siteName: 'Server Site', wordCount: 1000,
      publishedAt: '2026-09-30', originalUrl: s.canonicalUrl, totalSeconds: 180, maxProgress: 0.9, lastReadAt: '2026-10-06T12:00:00Z',
    })
  })

  test('keeps the larger total', () => {
    const local: LocalArticle = { ...meta, totalSeconds: 300, maxProgress: 0.4, lastReadAt: '2026-10-06T10:00:00Z' }
    const merged = restoreArticle(local, serverArticle())
    expect(merged).toMatchObject({ totalSeconds: 300, maxProgress: 0.9, lastReadAt: '2026-10-06T12:00:00Z' })
    expect(restoreArticle({ ...local, totalSeconds: 100 }, serverArticle()).totalSeconds).toBe(180)
  })

  test('keeps local descriptive fields and fills empty ones', () => {
    const local: LocalArticle = {
      ...meta, title: 'L', author: '', totalSeconds: 10, maxProgress: 0, lastReadAt: '2026-10-06T10:00:00Z',
    }
    const merged = restoreArticle(local, serverArticle())
    expect(merged.title).toBe('L')
    expect(merged.author).toBe('A')
    expect(merged.originalUrl).toBe(meta.originalUrl)
  })
})

describe('todayCarry', () => {
  test('is the excess of server over local, never negative', () => {
    expect(todayCarry(1800, 0)).toBe(1800)
    expect(todayCarry(1800, 300)).toBe(1500)
    expect(todayCarry(100, 300)).toBe(0)
  })
})
