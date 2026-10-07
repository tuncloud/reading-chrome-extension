import { describe, expect, test } from 'vitest'
import { chooseAdapter, hostInDomain, normalizeDomain, originPatterns } from './domains'

describe('normalizeDomain', () => {
  test.each([
    ['example.com', 'example.com'],
    ['  Example.COM ', 'example.com'],
    ['https://www.example.com/path?q=1', 'example.com'],
    ['blog.example.co.uk', 'blog.example.co.uk'],
  ])('%j → %s', (input, want) => {
    expect(normalizeDomain(input)).toBe(want)
  })

  test.each(['', 'localhost', 'not a domain', 'exa mple.com', '-bad.com', 'chrome://extensions'])('rejects %j', (input) => {
    expect(normalizeDomain(input)).toBeNull()
  })
})

test('originPatterns covers the domain and its subdomains over https', () => {
  expect(originPatterns('example.com')).toEqual(['https://example.com/*', 'https://*.example.com/*'])
})

test('hostInDomain', () => {
  expect(hostInDomain('example.com', 'example.com')).toBe(true)
  expect(hostInDomain('www.example.com', 'example.com')).toBe(true)
  expect(hostInDomain('badexample.com', 'example.com')).toBe(false)
})

describe('chooseAdapter', () => {
  const hosts = ['freedium.cfd']

  test('Medium and Freedium hosts use freedium-medium without being enabled', () => {
    expect(chooseAdapter('medium.com', hosts, { enabled: [], denied: [] })).toBe('freedium-medium')
    expect(chooseAdapter('freedium.cfd', hosts, { enabled: [], denied: [] })).toBe('freedium-medium')
  })

  test('the generic adapter runs only on enabled domains (C24)', () => {
    expect(chooseAdapter('blog.example.com', hosts, { enabled: [], denied: [] })).toBeNull()
    expect(chooseAdapter('blog.example.com', hosts, { enabled: ['example.com'], denied: [] })).toBe('generic')
  })

  test('T31: a denied domain is never tracked, even if enabled or Medium', () => {
    expect(chooseAdapter('blog.example.com', hosts, { enabled: ['example.com'], denied: ['example.com'] })).toBeNull()
    expect(chooseAdapter('medium.com', hosts, { enabled: [], denied: ['medium.com'] })).toBeNull()
  })
})
