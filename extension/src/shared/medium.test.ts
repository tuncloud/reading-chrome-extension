import { describe, expect, test } from 'vitest'
import { isFreediumHost, isMediumHost, mediumCanonical, mediumPostId } from './medium'

const hosts = ['freedium-mirror.cfd', 'freedium.cfd']

describe('mediumCanonical (T26)', () => {
  test('Freedium mirror wrapping a Medium URL', () => {
    expect(mediumCanonical(
      'https://freedium-mirror.cfd/https://medium.com/better-programming/designing-idempotent-apis-8f3c2a1b9d4e?source=x', hosts,
    )).toBe('https://medium.com/p/8f3c2a1b9d4e')
  })

  test('Medium author URL gives the same canonical', () => {
    expect(mediumCanonical('https://medium.com/@jane/foo-8f3c2a1b9d4e', hosts)).toBe('https://medium.com/p/8f3c2a1b9d4e')
  })

  test('Medium /p/<id> and subdomain publications', () => {
    expect(mediumCanonical('https://medium.com/p/8f3c2a1b9d4e', hosts)).toBe('https://medium.com/p/8f3c2a1b9d4e')
    expect(mediumCanonical('https://jane.medium.com/foo-8f3c2a1b9d4e?sk=1', hosts)).toBe('https://medium.com/p/8f3c2a1b9d4e')
  })

  test('scenario 23: a new mirror host from config gives the identical canonical', () => {
    const url = 'https://new-mirror.example/https://medium.com/x/designing-idempotent-apis-8f3c2a1b9d4e'
    expect(mediumCanonical(url, hosts)).toBeNull()
    expect(mediumCanonical(url, [...hosts, 'new-mirror.example'])).toBe('https://medium.com/p/8f3c2a1b9d4e')
  })

  test('Freedium wrapping a custom-domain Medium publication', () => {
    expect(mediumCanonical('https://freedium.cfd/https://betterprogramming.pub/some-title-0123456789ab', hosts))
      .toBe('https://medium.com/p/0123456789ab')
  })

  test.each([
    'https://medium.com/@jane',
    'https://medium.com/',
    'https://freedium.cfd/',
    'https://freedium.cfd/about',
    'https://example.com/foo-8f3c2a1b9d4e',
    'garbage',
  ])('no post id → null: %s', (url) => {
    expect(mediumCanonical(url, hosts)).toBeNull()
  })
})

describe('helpers', () => {
  test('isMediumHost', () => {
    expect(isMediumHost('medium.com')).toBe(true)
    expect(isMediumHost('Jane.Medium.com')).toBe(true)
    expect(isMediumHost('notmedium.com')).toBe(false)
  })

  test('isFreediumHost is exact and case-insensitive', () => {
    expect(isFreediumHost('Freedium.cfd', hosts)).toBe(true)
    expect(isFreediumHost('x.freedium.cfd', hosts)).toBe(false)
  })

  test('mediumPostId reads the last segment', () => {
    expect(mediumPostId('/a/b-8f3c2a1b9d4e/')).toBe('8f3c2a1b9d4e')
    expect(mediumPostId('/a/b-xyz')).toBeNull()
    expect(mediumPostId('/a/b-' + 'a'.repeat(32))).toBeNull()
    expect(mediumPostId('/p/8f3c2a1b9d4e')).toBe('8f3c2a1b9d4e')
    expect(mediumPostId('/a/designing-apis-8f3c2a1b9d4e')).toBe('8f3c2a1b9d4e')
  })
})
