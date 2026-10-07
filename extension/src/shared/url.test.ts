import { describe, expect, test } from 'vitest'
import { globMatch, normalizeUrl } from './url'

const strip = ['utm_*', 'fbclid', 'gclid', 'mc_cid', 'mc_eid', 'ref', 'source']

describe('globMatch', () => {
  test.each([
    ['utm_*', 'utm_source', true],
    ['utm_*', 'UTM_Medium', true],
    ['utm_*', 'xutm_source', false],
    ['ref', 'ref', true],
    ['ref', 'referrer', false],
    ['a.b', 'axb', false],
  ])('%s vs %s → %s', (p, n, want) => {
    expect(globMatch(p, n)).toBe(want)
  })
})

describe('normalizeUrl', () => {
  test('T26: strips tracking params, fragment and trailing slash', () => {
    expect(normalizeUrl('https://example.com/post/?utm_source=x#h', { stripQueryParams: strip })).toBe('https://example.com/post')
  })

  test('lowercases the host and keeps other params in order', () => {
    expect(normalizeUrl('HTTPS://Example.COM/a/?b=1&utm_medium=x&fbclid=y&c=2', { stripQueryParams: strip }))
      .toBe('https://example.com/a?b=1&c=2')
  })

  test('keeps the root slash', () => {
    expect(normalizeUrl('https://example.com/?ref=hn', { stripQueryParams: strip })).toBe('https://example.com/')
  })

  test('upgrades to https only when the site serves https', () => {
    expect(normalizeUrl('http://example.com/a', { stripQueryParams: strip })).toBe('http://example.com/a')
    expect(normalizeUrl('http://example.com/a', { stripQueryParams: strip, preferHttps: true })).toBe('https://example.com/a')
  })

  test('drops credentials', () => {
    expect(normalizeUrl('https://u:p@example.com/a', { stripQueryParams: strip })).toBe('https://example.com/a')
  })

  test.each(['not a url', 'ftp://example.com/a', 'chrome://settings', ''])('rejects %j', (raw) => {
    expect(normalizeUrl(raw, { stripQueryParams: strip })).toBeNull()
  })
})
