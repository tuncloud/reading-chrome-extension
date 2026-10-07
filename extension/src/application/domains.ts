// DomainService manages user-enabled and denied domains (spec T31, C24).
import type { RemoteConfig } from '../domain/config'
import { chooseAdapter, normalizeDomain, originPatterns, type AdapterName, type DomainLists } from '../domain/domains'
import type { ContentScripts, LocalStore, Permissions } from './ports'

export const SCRIPT_PREFIX = 'generic:'

export type EnableResult =
  | { ok: true; domain: string }
  | { ok: false; reason: 'invalid' | 'permission_denied' | 'already_enabled' }

export interface DomainDeps {
  store: LocalStore
  permissions: Permissions
  scripts: ContentScripts
}

export class DomainService {
  constructor(private readonly deps: DomainDeps) {}

  async lists(): Promise<DomainLists> {
    const { store } = this.deps
    return { enabled: (await store.getKv('enabled_domains')) ?? [], denied: (await store.getKv('denied_domains')) ?? [] }
  }

  // enable must run from a click handler: the permission prompt is the first awaited call,
  // because Chrome grants permissions.request only inside the user gesture.
  async enable(input: string): Promise<EnableResult> {
    const domain = normalizeDomain(input)
    if (!domain) return { ok: false, reason: 'invalid' }
    const origins = originPatterns(domain)
    if (!(await this.deps.permissions.request(origins))) return { ok: false, reason: 'permission_denied' }
    const { enabled } = await this.lists()
    if (enabled.includes(domain)) return { ok: false, reason: 'already_enabled' }
    await this.deps.scripts.register(SCRIPT_PREFIX + domain, origins)
    await this.deps.store.setKv('enabled_domains', [...enabled, domain].sort())
    return { ok: true, domain }
  }

  async disable(domain: string): Promise<void> {
    await this.deps.scripts.unregister([SCRIPT_PREFIX + domain])
    await this.deps.permissions.remove(originPatterns(domain))
    const { enabled } = await this.lists()
    await this.deps.store.setKv('enabled_domains', enabled.filter((d) => d !== domain))
  }

  async deny(input: string): Promise<{ ok: true; domain: string } | { ok: false; reason: 'invalid' }> {
    const domain = normalizeDomain(input)
    if (!domain) return { ok: false, reason: 'invalid' }
    const { denied } = await this.lists()
    if (!denied.includes(domain)) await this.deps.store.setKv('denied_domains', [...denied, domain].sort())
    return { ok: true, domain }
  }

  async undeny(domain: string): Promise<void> {
    const { denied } = await this.lists()
    await this.deps.store.setKv('denied_domains', denied.filter((d) => d !== domain))
  }

  // reconcile makes registered generic content scripts match enabled_domains.
  async reconcile(): Promise<void> {
    const { scripts } = this.deps
    const { enabled } = await this.lists()
    const registered = (await scripts.registeredIds()).filter((id) => id.startsWith(SCRIPT_PREFIX))
    const wanted = new Set(enabled.map((d) => SCRIPT_PREFIX + d))
    const stale = registered.filter((id) => !wanted.has(id))
    if (stale.length > 0) await scripts.unregister(stale)
    for (const d of enabled) {
      if (!registered.includes(SCRIPT_PREFIX + d)) await scripts.register(SCRIPT_PREFIX + d, originPatterns(d))
    }
  }

  async plan(host: string, config: RemoteConfig): Promise<AdapterName | null> {
    return chooseAdapter(host, config.freediumHosts, await this.lists())
  }
}
