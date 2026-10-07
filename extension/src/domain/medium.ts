// Medium identity (spec T26): every Medium URL and every Freedium mirror of it maps to
// https://medium.com/p/<post id>, so reading on any of them counts for one article.

const POST_ID = /-?([0-9a-f]{10,16})$/

export function isMediumHost(host: string): boolean {
  const h = host.toLowerCase()
  return h === 'medium.com' || h.endsWith('.medium.com')
}

export function isFreediumHost(host: string, freediumHosts: readonly string[]): boolean {
  const h = host.toLowerCase()
  return freediumHosts.some((f) => f.toLowerCase() === h)
}

// mediumPostId reads the post id from the last path segment (…-8f3c2a1b9d4e or /p/8f3c2a1b9d4e).
export function mediumPostId(path: string): string | null {
  const last = path.split('/').filter(Boolean).pop() ?? ''
  return POST_ID.exec(last)?.[1] ?? null
}

// mediumCanonical returns https://medium.com/p/<id> for a Medium page or a Freedium page
// wrapping one (/https://…), or null when the URL carries no post id.
export function mediumCanonical(raw: string, freediumHosts: readonly string[]): string | null {
  let u: URL
  try {
    u = new URL(raw)
  } catch {
    return null
  }
  let path: string
  if (isFreediumHost(u.hostname, freediumHosts)) {
    const inner = u.pathname.slice(1)
    if (!/^https?:/i.test(inner)) return null
    try {
      path = new URL(inner).pathname
    } catch {
      return null
    }
  } else if (isMediumHost(u.hostname)) {
    path = u.pathname
  } else {
    return null
  }
  const id = mediumPostId(path)
  return id ? `https://medium.com/p/${id}` : null
}
