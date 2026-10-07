// @vitest-environment jsdom
import { describe, expect, test } from 'vitest'
import { wireConfig } from '../../../test/fixtures'
import { parseRemoteConfig } from '../../domain/config'
import { canonicalFromDocument, countWords, defaultContentRoot, finishMeta, jsonLdArticle } from './metadata'

const config = parseRemoteConfig(wireConfig)
const page = (html: string) => new DOMParser().parseFromString(html, 'text/html')

describe('canonicalFromDocument', () => {
  test('link rel=canonical wins, resolved against the page and normalized', () => {
    const doc = page('<link rel="canonical" href="/post/?utm_source=x"><meta property="og:url" content="https://e.com/og">')
    expect(canonicalFromDocument(doc, 'https://e.com/post/?ref=hn#top', config)).toBe('https://e.com/post')
  })

  test('og:url next, then the location', () => {
    expect(canonicalFromDocument(page('<meta property="og:url" content="https://e.com/og/">'), 'https://e.com/x', config)).toBe('https://e.com/og')
    expect(canonicalFromDocument(page(''), 'https://E.com/x/?fbclid=1', config)).toBe('https://e.com/x')
  })

  test('an http canonical on an https page is upgraded', () => {
    expect(canonicalFromDocument(page('<link rel="canonical" href="http://e.com/a">'), 'https://e.com/a', config)).toBe('https://e.com/a')
  })

  test('a junk canonical falls through to the next candidate', () => {
    expect(canonicalFromDocument(page('<link rel="canonical" href="javascript:void(0)">'), 'https://e.com/a', config)).toBe('https://e.com/a')
  })
})

describe('jsonLdArticle', () => {
  test('finds an article inside @graph with an author object', () => {
    const doc = page(`<script type="application/ld+json">${JSON.stringify({
      '@context': 'https://schema.org',
      '@graph': [{ '@type': 'WebSite', name: 'x' }, { '@type': ['BlogPosting'], headline: 'H', author: { '@type': 'Person', name: 'Ann' },
        datePublished: '2026-09-30T08:00:00Z', wordCount: '1200' }],
    })}</script>`)
    expect(jsonLdArticle(doc)).toEqual({ headline: 'H', author: 'Ann', datePublished: '2026-09-30T08:00:00Z', wordCount: 1200 })
  })

  test('reads an author array and skips broken JSON', () => {
    const doc = page(`<script type="application/ld+json">{ not json</script>
      <script type="application/ld+json">[{"@type":"NewsArticle","headline":"N","author":[{"name":"A"},{"name":"B"}]}]</script>`)
    expect(jsonLdArticle(doc)).toEqual({ headline: 'N', author: 'A', datePublished: '', wordCount: 0 })
  })

  test('no article → empty fields', () => {
    expect(jsonLdArticle(page('<p>hi</p>'))).toEqual({ headline: '', author: '', datePublished: '', wordCount: 0 })
  })
})

test('content root is article, then main, then body', () => {
  expect(defaultContentRoot(page('<main><article id="a"></article></main>')).id).toBe('a')
  expect(defaultContentRoot(page('<main id="m"></main>')).id).toBe('m')
  expect(defaultContentRoot(page('<p></p>')).tagName).toBe('BODY')
})

test('countWords counts words per text node, across blocks, skipping scripts', () => {
  const doc = page('<article><h1>Two words</h1><p>and  three\nmore</p><script>var a = 1</script></article>')
  expect(countWords(defaultContentRoot(doc))).toBe(5)
})

describe('finishMeta', () => {
  const base = {
    canonicalUrl: 'https://e.com/a', source: 'web', title: '  T  ', author: 'A', siteName: 'S',
    wordCount: 10, publishedAt: '2026-09-30', originalUrl: 'https://e.com/a?x=1',
  }

  test('trims and truncates text to the backend limits', () => {
    const m = finishMeta({ ...base, title: 'x'.repeat(600), author: 'y'.repeat(300) })!
    expect(m.title).toHaveLength(500)
    expect(m.author).toHaveLength(200)
    expect(finishMeta(base)?.title).toBe('T')
  })

  test('clamps word count to 0…200000', () => {
    expect(finishMeta({ ...base, wordCount: 999_999 })?.wordCount).toBe(200_000)
    expect(finishMeta({ ...base, wordCount: -3 })?.wordCount).toBe(0)
    expect(finishMeta({ ...base, wordCount: Number.NaN })?.wordCount).toBe(0)
  })

  test('a canonical URL over 2048 chars cannot be tracked', () => {
    expect(finishMeta({ ...base, canonicalUrl: `https://e.com/${'a'.repeat(2048)}` })).toBeNull()
  })

  test('an over-long original URL is cut back to the canonical', () => {
    expect(finishMeta({ ...base, originalUrl: `https://e.com/${'a'.repeat(3000)}` })?.originalUrl).toBe('https://e.com/a')
  })
})
