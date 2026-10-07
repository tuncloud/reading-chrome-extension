import { expect, test } from 'vitest'
import { readingProgress } from './progress'

test('progress is the share of the content root above the viewport bottom', () => {
  expect(readingProgress({ scrollY: 0, viewportHeight: 800, rootTop: 200, rootHeight: 3000 })).toBeCloseTo(0.2)
  expect(readingProgress({ scrollY: 1400, viewportHeight: 800, rootTop: 200, rootHeight: 3000 })).toBeCloseTo(0.6667, 3)
})

test('progress is clamped to [0, 1]', () => {
  expect(readingProgress({ scrollY: 0, viewportHeight: 100, rootTop: 500, rootHeight: 3000 })).toBe(0)
  expect(readingProgress({ scrollY: 9000, viewportHeight: 800, rootTop: 0, rootHeight: 3000 })).toBe(1)
})

test('an empty root reads as 0, never NaN', () => {
  expect(readingProgress({ scrollY: 0, viewportHeight: 800, rootTop: 0, rootHeight: 0 })).toBe(0)
  expect(readingProgress({ scrollY: Number.NaN, viewportHeight: 800, rootTop: 0, rootHeight: 3000 })).toBe(0)
})
