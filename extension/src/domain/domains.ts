// Which sites are tracked (spec T26, T31, C24): Medium/Freedium always, other sites only
// when the user enabled their domain, and never a denied domain.
import { isFreediumHost, isMediumHost } from './medium'

export type AdapterName = 'freedium-medium' | 'generic'

const HOSTNAME = /^(?=.{1,253}$)[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$/

// normalizeDomain turns user input ("https://www.Example.com/x") into "example.com",
// or null when it is not a dotted hostname.
export function normalizeDomain(input: string): string | null {
  let s = input.trim().toLowerCase()
  if (!s) return null
  if (!/^[a-z][a-z0-9+.-]*:\/\//.test(s)) s = `https://${s}`
  let host: string
  try {
    const u = new URL(s)
    if (u.protocol !== 'https:' && u.protocol !== 'http:') return null
    host = u.hostname
  } catch {
    return null
  }
  host = host.replace(/^www\./, '').replace(/\.$/, '')
  return HOSTNAME.test(host) ? host : null
}

// originPatterns are the host permissions and content-script matches for an enabled domain.
export function originPatterns(domain: string): string[] {
  return [`https://${domain}/*`, `https://*.${domain}/*`]
}

export function hostInDomain(host: string, domain: string): boolean {
  const h = host.toLowerCase()
  return h === domain || h.endsWith(`.${domain}`)
}

export interface DomainLists {
  enabled: readonly string[]
  denied: readonly string[]
}

export function chooseAdapter(host: string, freediumHosts: readonly string[], lists: DomainLists): AdapterName | null {
  if (lists.denied.some((d) => hostInDomain(host, d))) return null
  if (isMediumHost(host) || isFreediumHost(host, freediumHosts)) return 'freedium-medium'
  if (lists.enabled.some((d) => hostInDomain(host, d))) return 'generic'
  return null
}
