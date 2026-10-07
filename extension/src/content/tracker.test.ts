import { describe, expect, test } from 'vitest'
import { Tracker, type SegmentDraft, type TrackerOptions } from './tracker'

const S = 1000

function ids(): () => string {
  let n = 0
  return () => `id-${++n}`
}

function tracker(over: Partial<TrackerOptions> = {}): Tracker {
  return new Tracker({ idleSeconds: 60, segmentSeconds: 60, now: 0, visible: true, focused: true, newId: ids(), ...over })
}

// run ticks once per second over (from, to], optionally with input every `inputEvery` seconds.
function run(t: Tracker, from: number, to: number, inputEvery = 0): SegmentDraft[] {
  const out: SegmentDraft[] = []
  for (let now = from + S; now <= to; now += S) {
    if (inputEvery && (now / S) % inputEvery === 0) out.push(...t.input(now))
    out.push(...t.tick(now))
  }
  return out
}

const seconds = (segs: SegmentDraft[]) => segs.map((s) => s.activeSeconds)

describe('T27 acceptance', () => {
  test('a hidden tab accrues 0 s', () => {
    const t = tracker({ visible: false })
    expect(t.state).toBe('hidden')
    expect(run(t, 0, 120 * S, 5)).toEqual([])
    expect(t.pagehide(120 * S)).toEqual([])
  })

  test('2 min with no input accrues exactly idle_seconds', () => {
    const t = tracker()
    const segs = [...run(t, 0, 120 * S), ...t.pagehide(120 * S)]
    expect(segs.reduce((n, s) => n + s.activeSeconds, 0)).toBe(60)
    expect(t.state).toBe('stopped')
  })

  test('150 s active with segment_seconds=60 → 60, 60, 30 on pagehide', () => {
    const t = tracker()
    const segs = [...run(t, 0, 150 * S, 10), ...t.pagehide(150 * S)]
    expect(seconds(segs)).toEqual([60, 60, 30])
    expect(segs.map((s) => s.seq)).toEqual([0, 1, 2])
  })
})

describe('segments', () => {
  test('carry session, ids, contiguous times and the max progress', () => {
    const t = tracker()
    t.progress(0.3)
    t.progress(0.1)
    const [seg] = [...run(t, 0, 5 * S), ...t.pagehide(5 * S)]
    expect(seg).toEqual({
      id: 'id-2', sessionId: 'id-1', seq: 0, startedAt: 0, endedAt: 5 * S, activeSeconds: 5, maxProgress: 0.3,
    })
  })

  test('active_seconds never exceeds (ended − started) + 5 (backend validation)', () => {
    const t = tracker()
    for (const s of [...run(t, 0, 150 * S, 10), ...t.pagehide(150 * S)]) {
      expect(s.activeSeconds).toBeLessThanOrEqual((s.endedAt - s.startedAt) / S + 5)
      expect(s.activeSeconds).toBeGreaterThan(0)
    }
  })

  test('leaving active flushes what was read so far', () => {
    const t = tracker()
    run(t, 0, 10 * S)
    expect(seconds(t.setFocused(false, 10 * S))).toEqual([10])
    expect(t.state).toBe('hidden')
  })

  test('a transition with nothing accrued sends no segment (C23)', () => {
    const t = tracker()
    expect(t.setVisible(false, 0)).toEqual([])
    expect(t.pagehide(0)).toEqual([])
  })

  test('idle ends the segment; input resumes a new one', () => {
    const t = tracker()
    const first = run(t, 0, 70 * S) // 60 s then idle
    expect(seconds(first)).toEqual([60])
    expect(t.state).toBe('idle')
    t.input(70 * S)
    expect(t.state).toBe('active')
    const second = [...run(t, 70 * S, 80 * S), ...t.pagehide(80 * S)]
    expect(second).toHaveLength(1)
    expect(second[0]).toMatchObject({ activeSeconds: 10, seq: 1, startedAt: 70 * S, endedAt: 80 * S })
  })

  test('idle stays idle without input', () => {
    const t = tracker({ idleSeconds: 5 })
    run(t, 0, 10 * S)
    expect(t.state).toBe('idle')
    expect(run(t, 10 * S, 30 * S)).toEqual([])
  })

  test('hidden → visible and focused is active again and counts as input', () => {
    const t = tracker()
    t.setVisible(false, 0)
    run(t, 0, 100 * S)
    t.setVisible(true, 100 * S)
    expect(t.state).toBe('active')
    expect(seconds([...run(t, 100 * S, 130 * S), ...t.pagehide(130 * S)])).toEqual([30])
  })

  test('visible but blurred stays hidden', () => {
    const t = tracker({ focused: false })
    t.setVisible(true, 0)
    expect(t.state).toBe('hidden')
    t.setFocused(true, S)
    expect(t.state).toBe('active')
  })
})

describe('sessions', () => {
  test('newSession flushes under the old session, then restarts seq and progress', () => {
    const t = tracker()
    t.progress(0.8)
    run(t, 0, 10 * S)
    const old = t.sessionId
    const flushed = t.newSession(10 * S)
    expect(flushed).toHaveLength(1)
    expect(flushed[0]).toMatchObject({ sessionId: old, seq: 0, maxProgress: 0.8 })
    expect(t.sessionId).not.toBe(old)
    const next = [...run(t, 10 * S, 15 * S), ...t.pagehide(15 * S)]
    expect(next[0]).toMatchObject({ sessionId: t.sessionId, seq: 0, maxProgress: 0 })
  })
})

test('stopped ignores every event', () => {
  const t = tracker()
  t.pagehide(0)
  expect(t.input(S)).toEqual([])
  expect(t.setVisible(true, S)).toEqual([])
  expect(run(t, 0, 10 * S, 1)).toEqual([])
  expect(t.pagehide(10 * S)).toEqual([])
  expect(t.state).toBe('stopped')
})
