// @vitest-environment jsdom
import { expect, test } from 'vitest'
import { wireConfig } from '../../../test/fixtures'
import { parseRemoteConfig } from '../../domain/config'
import { freediumMedium } from './freedium-medium'

const config = parseRemoteConfig(wireConfig)
const page = (html: string) => new DOMParser().parseFromString(html, 'text/html')
const body = `<article><h1>Designing Idempotent APIs</h1><p>${'word '.repeat(298)}</p></article>`

test('T26: a Freedium page maps to the Medium canonical with its metadata', () => {
  const location = 'https://freedium-mirror.cfd/https://medium.com/better-programming/designing-idempotent-apis-8f3c2a1b9d4e?source=x'
  const doc = page(`<head><title>Freedium</title><meta name="author" content="Jane Doe">
    <meta property="og:site_name" content="Better Programming"><meta property="article:published_time" content="2026-09-30T08:00:00Z"></head>
    <body>${body}</body>`)
  expect(freediumMedium.extract({ doc, location, config })).toEqual({
    canonicalUrl: 'https://medium.com/p/8f3c2a1b9d4e', source: 'medium', title: 'Designing Idempotent APIs', author: 'Jane Doe',
    siteName: 'Better Programming', wordCount: 301, publishedAt: '2026-09-30T08:00:00Z', originalUrl: location,
  })
})

test('T26: the Medium author URL gives the same canonical', () => {
  const meta = freediumMedium.extract({ doc: page(body), location: 'https://medium.com/@jane/foo-8f3c2a1b9d4e', config })
  expect(meta?.canonicalUrl).toBe('https://medium.com/p/8f3c2a1b9d4e')
})

test('no post id falls back to link rel=canonical, og:title and a[rel=author]', () => {
  const doc = page(`<head><link rel="canonical" href="https://medium.com/some-list/?source=feed">
    <meta property="og:title" content="A list"></head><body><a rel="author" href="/@x">Ann</a><main>${'w '.repeat(10)}</main></body>`)
  expect(freediumMedium.extract({ doc, location: 'https://medium.com/some-list', config })).toMatchObject({
    canonicalUrl: 'https://medium.com/some-list', title: 'A list', author: 'Ann', wordCount: 10,
  })
})
