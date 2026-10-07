// URL normalization (spec T26): the canonical URL is the article's identity on every device.

// globMatch matches a query-param name against a pattern where * is any run of characters.
export function globMatch(pattern: string, name: string): boolean {
  const body = pattern.split('*').map((part) => part.replace(/[.+?^${}()|[\]\\]/g, '\\$&')).join('.*')
  return new RegExp(`^${body}$`, 'i').test(name)
}

export interface NormalizeOptions {
  stripQueryParams: readonly string[]
  // preferHttps upgrades http:// when the page itself is served over https.
  preferHttps?: boolean
}

// normalizeUrl lowercases the host, drops the fragment, credentials and matching query
// params, and removes a trailing slash (except the root). Non-http(s) input gives null.
export function normalizeUrl(raw: string, opts: NormalizeOptions): string | null {
  let u: URL
  try {
    u = new URL(raw)
  } catch {
    return null
  }
  if (u.protocol !== 'http:' && u.protocol !== 'https:') return null
  if (opts.preferHttps && u.protocol === 'http:') u.protocol = 'https:'
  u.hash = ''
  u.username = ''
  u.password = ''
  const kept = [...u.searchParams].filter(([name]) => !opts.stripQueryParams.some((p) => globMatch(p, name)))
  u.search = kept.length > 0 ? `?${new URLSearchParams(kept).toString()}` : ''
  if (u.pathname.length > 1 && u.pathname.endsWith('/')) u.pathname = u.pathname.replace(/\/+$/, '')
  return u.toString()
}
