// PKCE S256 over WebCrypto (spec §2.1, T29): a 64-char verifier and base64url(SHA-256(verifier)).

const UNRESERVED = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~'
// The largest multiple of 66 below 256; bytes at or above it are redrawn to avoid modulo bias.
const LIMIT = 256 - (256 % UNRESERVED.length)

export function base64url(bytes: Uint8Array): string {
  let s = ''
  for (const b of bytes) s += String.fromCharCode(b)
  return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

export function randomVerifier(length = 64): string {
  let out = ''
  while (out.length < length) {
    for (const b of crypto.getRandomValues(new Uint8Array(length))) {
      if (b < LIMIT && out.length < length) out += UNRESERVED[b % UNRESERVED.length]
    }
  }
  return out
}

export async function challengeFor(verifier: string): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(verifier))
  return base64url(new Uint8Array(digest))
}
