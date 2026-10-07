// DOM readers shared by the site adapters.
import type { RemoteConfig } from '../../shared/config'
import type { ArticleMeta } from '../../shared/reading'
import { normalizeUrl } from '../../shared/url'

// Backend limits (backend/internal/usecase/readsync/validate.go).
const MAX_URL = 2048
const MAX_TITLE = 500
const MAX_NAME = 200
const MAX_WORDS = 200_000
const ARTICLE_TYPES = new Set(['Article', 'BlogPosting', 'NewsArticle'])

export function metaContent(doc: Document, selector: string): string {
  return doc.querySelector(selector)?.getAttribute('content')?.trim() ?? ''
}

export function firstText(doc: Document, selector: string): string {
  return doc.querySelector(selector)?.textContent?.replace(/\s+/g, ' ').trim() ?? ''
}

export function defaultContentRoot(doc: Document): Element {
  return doc.querySelector('article') ?? doc.querySelector('main') ?? doc.body ?? doc.documentElement
}

// countWords counts words per text node, so adjacent blocks (<h1>a</h1><p>b</p>) do not
// merge into one word the way textContent would; script and style text is skipped.
export function countWords(root: Element): number {
  const walker = root.ownerDocument.createTreeWalker(root, 4 /* NodeFilter.SHOW_TEXT */)
  let n = 0
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const tag = node.parentElement?.tagName
    if (tag === 'SCRIPT' || tag === 'STYLE' || tag === 'NOSCRIPT') continue
    n += (node.nodeValue ?? '').split(/\s+/).filter(Boolean).length
  }
  return n
}

// canonicalFromDocument tries link[rel=canonical], og:url, then the location, and returns
// the first that normalizes.
export function canonicalFromDocument(doc: Document, location: string, config: RemoteConfig): string | null {
  const preferHttps = location.startsWith('https:')
  const candidates = [doc.querySelector('link[rel="canonical"]')?.getAttribute('href') ?? '', metaContent(doc, 'meta[property="og:url"]'), location]
  for (const raw of candidates) {
    if (!raw.trim()) continue
    let absolute: string
    try {
      absolute = new URL(raw.trim(), location).toString()
    } catch {
      continue
    }
    const url = normalizeUrl(absolute, { stripQueryParams: config.stripQueryParams, preferHttps })
    if (url) return url
  }
  return null
}

export interface LdArticle {
  headline: string
  author: string
  datePublished: string
  wordCount: number
}

function nodes(value: unknown): Record<string, unknown>[] {
  if (Array.isArray(value)) return value.flatMap(nodes)
  if (typeof value !== 'object' || value === null) return []
  const obj = value as Record<string, unknown>
  return [obj, ...nodes(obj['@graph'])]
}

function isArticle(node: Record<string, unknown>): boolean {
  const t = node['@type']
  return (Array.isArray(t) ? t : [t]).some((x) => typeof x === 'string' && ARTICLE_TYPES.has(x))
}

function authorName(a: unknown): string {
  if (typeof a === 'string') return a.trim()
  if (Array.isArray(a)) return a.length > 0 ? authorName(a[0]) : ''
  if (typeof a === 'object' && a !== null && typeof (a as { name?: unknown }).name === 'string') return ((a as { name: string }).name).trim()
  return ''
}

// jsonLdArticle reads the first Article/BlogPosting/NewsArticle in the page's JSON-LD.
export function jsonLdArticle(doc: Document): LdArticle {
  for (const script of doc.querySelectorAll('script[type="application/ld+json"]')) {
    let parsed: unknown
    try {
      parsed = JSON.parse(script.textContent ?? '')
    } catch {
      continue
    }
    const node = nodes(parsed).find(isArticle)
    if (!node) continue
    const words = Number(node.wordCount)
    return {
      headline: typeof node.headline === 'string' ? node.headline.trim() : '',
      author: authorName(node.author),
      datePublished: typeof node.datePublished === 'string' ? node.datePublished.trim() : '',
      wordCount: Number.isFinite(words) && words > 0 ? Math.floor(words) : 0,
    }
  }
  return { headline: '', author: '', datePublished: '', wordCount: 0 }
}

const MAX_DATE = 64

function cut(s: string, max: number): string {
  const t = s.replace(/\s+/g, ' ').trim()
  return [...t].slice(0, max).join('')
}

// cleanPublishedAt keeps a date the backend can parse; anything else would get the whole batch rejected.
function cleanPublishedAt(raw: string): string {
  const t = raw.trim()
  return t.length <= MAX_DATE && Number.isFinite(Date.parse(t)) ? t : ''
}

// finishMeta applies the backend's limits so neither the article nor its segments get rejected.
export function finishMeta(m: ArticleMeta): ArticleMeta | null {
  if (m.canonicalUrl.length > MAX_URL) return null
  const words = Number.isFinite(m.wordCount) ? Math.floor(m.wordCount) : 0
  return {
    canonicalUrl: m.canonicalUrl,
    source: m.source,
    title: cut(m.title, MAX_TITLE),
    author: cut(m.author, MAX_NAME),
    siteName: cut(m.siteName, MAX_NAME),
    wordCount: Math.min(MAX_WORDS, Math.max(0, words)),
    publishedAt: cleanPublishedAt(m.publishedAt),
    originalUrl: m.originalUrl.length > MAX_URL ? m.canonicalUrl : m.originalUrl,
  }
}
