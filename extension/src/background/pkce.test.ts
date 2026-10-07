import { expect, test } from 'vitest'
import { base64url, challengeFor, randomVerifier } from './pkce'

test('RFC 7636 appendix B vector', async () => {
  expect(await challengeFor('dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk')).toBe('E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM')
})

test('T29: verifiers are 64 unreserved characters and differ', () => {
  const a = randomVerifier()
  expect(a).toMatch(/^[A-Za-z0-9\-._~]{64}$/)
  expect(randomVerifier()).not.toBe(a)
})

test('base64url has no padding or +/', () => {
  expect(base64url(new Uint8Array([251, 255, 191]))).toBe('-_-_')
})
