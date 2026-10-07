// Local reading records (spec §3.4) and the rules that combine them.

export interface ArticleMeta {
  canonicalUrl: string
  source: string
  title: string
  author: string
  siteName: string
  wordCount: number
  publishedAt: string
  originalUrl: string
}

// Segment is one stretch of active reading (spec §2.8 segments[]); times are RFC 3339 UTC.
export interface Segment {
  id: string
  sessionId: string
  seq: number
  canonicalUrl: string
  startedAt: string
  endedAt: string
  activeSeconds: number
  maxProgress: number
}

export interface LocalArticle extends ArticleMeta {
  totalSeconds: number
  maxProgress: number
  lastReadAt: string
}

export interface StoredSegment extends Segment {
  synced: 0 | 1
  createdAt: string
}

function keep(next: string, prev: string | undefined): string {
  return next.trim() ? next : (prev ?? '')
}

// mergeArticle folds one segment into the local article. Metadata found earlier is kept
// when a later extraction comes back empty.
export function mergeArticle(prev: LocalArticle | undefined, meta: ArticleMeta, seg: Segment): LocalArticle {
  return {
    canonicalUrl: meta.canonicalUrl,
    source: keep(meta.source, prev?.source),
    title: keep(meta.title, prev?.title),
    author: keep(meta.author, prev?.author),
    siteName: keep(meta.siteName, prev?.siteName),
    wordCount: meta.wordCount > 0 ? meta.wordCount : (prev?.wordCount ?? 0),
    publishedAt: keep(meta.publishedAt, prev?.publishedAt),
    originalUrl: keep(meta.originalUrl, prev?.originalUrl),
    totalSeconds: (prev?.totalSeconds ?? 0) + seg.activeSeconds,
    maxProgress: Math.max(prev?.maxProgress ?? 0, seg.maxProgress),
    lastReadAt: !prev || seg.endedAt > prev.lastReadAt ? seg.endedAt : prev.lastReadAt,
  }
}

// roundMinutes matches the backend's rounding (half away from zero; inputs are ≥ 0).
export function roundMinutes(seconds: number): number {
  return Math.round(seconds / 60)
}

// localDate is the calendar date (YYYY-MM-DD) of an instant in a time zone.
export function localDate(at: Date, timeZone: string): string {
  return new Intl.DateTimeFormat('en-CA', { timeZone, year: 'numeric', month: '2-digit', day: '2-digit' }).format(at)
}

// todaySeconds sums segments that started on today's local date.
export function todaySeconds(segments: readonly Segment[], now: Date, timeZone: string): number {
  const today = localDate(now, timeZone)
  return segments
    .filter((s) => localDate(new Date(s.startedAt), timeZone) === today)
    .reduce((sum, s) => sum + s.activeSeconds, 0)
}
