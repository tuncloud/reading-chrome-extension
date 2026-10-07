// @vitest-environment jsdom
import { expect, test } from 'vitest'
import { wireConfig } from '../../../test/fixtures'
import { parseRemoteConfig } from '../../domain/config'
import { generic } from './generic'
import { adapterFor } from './registry'

const config = parseRemoteConfig(wireConfig)
const page = (html: string) => new DOMParser().parseFromString(html, 'text/html')

test('T26: JSON-LD BlogPosting wins over OpenGraph', () => {
  const doc = page(`<head><link rel="canonical" href="https://blog.example.com/p/1/">
    <meta property="og:title" content="OG title"><meta property="og:site_name" content="Example Blog">
    <script type="application/ld+json">{"@type":"BlogPosting","headline":"LD title","author":{"name":"Ann"},
      "datePublished":"2026-09-30","wordCount":1234}</script></head><body><article>short</article></body>`)
  expect(generic.extract({ doc, location: 'https://blog.example.com/p/1/?utm_campaign=z', config })).toEqual({
    canonicalUrl: 'https://blog.example.com/p/1', source: 'web', title: 'LD title', author: 'Ann', siteName: 'Example Blog',
    wordCount: 1234, publishedAt: '2026-09-30', originalUrl: 'https://blog.example.com/p/1/?utm_campaign=z',
  })
})

test('without JSON-LD it uses OpenGraph, meta author and counts words; site defaults to the host', () => {
  const doc = page(`<head><title>Doc title</title><meta property="og:title" content="OG title">
    <meta name="author" content="Bob"><meta property="article:published_time" content="2026-09-29T10:00:00Z"></head>
    <body><nav>menu menu</nav><article>${'w '.repeat(42)}</article></body>`)
  expect(generic.extract({ doc, location: 'https://www.example.com/a', config })).toMatchObject({
    canonicalUrl: 'https://www.example.com/a', title: 'OG title', author: 'Bob', siteName: 'example.com',
    wordCount: 42, publishedAt: '2026-09-29T10:00:00Z',
  })
})

test('falls back to document.title', () => {
  expect(generic.extract({ doc: page('<title>Only title</title><p>x</p>'), location: 'https://e.com/a', config })?.title).toBe('Only title')
})

test('registry returns each adapter by name', () => {
  expect(adapterFor('generic')).toBe(generic)
  expect(adapterFor('freedium-medium').name).toBe('freedium-medium')
})
