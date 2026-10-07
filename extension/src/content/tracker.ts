// The per-tab reading tracker (onboarding §5.4, spec T27), free of DOM and timers: the
// driver feeds it events and a 1 s tick, and sends whatever segments it flushes.

export type TrackerState = 'active' | 'idle' | 'hidden' | 'stopped'

export interface TrackerOptions {
  idleSeconds: number
  segmentSeconds: number
  // now is epoch milliseconds at page load.
  now: number
  visible: boolean
  focused: boolean
  newId: () => string
}

// SegmentDraft is a flushed segment before it gets its URL; times are epoch ms.
export interface SegmentDraft {
  id: string
  sessionId: string
  seq: number
  startedAt: number
  endedAt: number
  activeSeconds: number
  maxProgress: number
}

const SECOND = 1000

export class Tracker {
  private current: TrackerState
  private visible: boolean
  private focused: boolean
  private lastInput: number
  private session: string
  private seq = 0
  private maxProgress = 0
  private segStart: number | null = null
  private segEnd = 0
  private segSeconds = 0

  constructor(private readonly opts: TrackerOptions) {
    this.visible = opts.visible
    this.focused = opts.focused
    this.lastInput = opts.now
    this.session = opts.newId()
    this.current = this.visible && this.focused ? 'active' : 'hidden'
  }

  get state(): TrackerState {
    return this.current
  }

  get sessionId(): string {
    return this.session
  }

  input(now: number): SegmentDraft[] {
    if (this.current === 'stopped') return []
    this.lastInput = now
    if (this.current === 'idle') this.current = 'active'
    return []
  }

  setVisible(visible: boolean, now: number): SegmentDraft[] {
    this.visible = visible
    return this.reconcile(now)
  }

  setFocused(focused: boolean, now: number): SegmentDraft[] {
    this.focused = focused
    return this.reconcile(now)
  }

  progress(p: number): void {
    if (p > this.maxProgress) this.maxProgress = p
  }

  tick(now: number): SegmentDraft[] {
    if (this.current !== 'active') return []
    if (now - this.lastInput > this.opts.idleSeconds * SECOND) {
      const out = this.flush()
      this.current = 'idle'
      return out
    }
    if (this.segStart === null) this.segStart = now - SECOND
    this.segSeconds += 1
    this.segEnd = now
    return this.segSeconds >= this.opts.segmentSeconds ? this.flush() : []
  }

  pagehide(_now: number): SegmentDraft[] {
    if (this.current === 'stopped') return []
    const out = this.flush()
    this.current = 'stopped'
    return out
  }

  // newSession starts a new reading session (SPA navigation): the open segment is flushed
  // under the old session, then seq and progress restart.
  newSession(now: number): SegmentDraft[] {
    if (this.current === 'stopped') return []
    const out = this.flush()
    this.session = this.opts.newId()
    this.seq = 0
    this.maxProgress = 0
    this.lastInput = now
    if (this.current === 'idle') this.current = 'active'
    return out
  }

  private reconcile(now: number): SegmentDraft[] {
    if (this.current === 'stopped') return []
    const shown = this.visible && this.focused
    if (!shown && this.current !== 'hidden') {
      const out = this.flush()
      this.current = 'hidden'
      return out
    }
    if (shown && this.current === 'hidden') {
      this.current = 'active'
      this.lastInput = now
    }
    return []
  }

  private flush(): SegmentDraft[] {
    const start = this.segStart
    const secs = this.segSeconds
    this.segStart = null
    this.segSeconds = 0
    if (start === null || secs < 1) return []
    return [{
      id: this.opts.newId(),
      sessionId: this.session,
      seq: this.seq++,
      startedAt: start,
      endedAt: this.segEnd,
      activeSeconds: secs,
      maxProgress: this.maxProgress,
    }]
  }
}
