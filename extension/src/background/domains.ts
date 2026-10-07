// DomainService manages user-enabled and denied domains (spec T31, C24).
import type { RemoteConfig } from '../shared/config'
import { chooseAdapter, normalizeDomain, originPatterns, type AdapterName, type DomainLists } from '../shared/domains'
import type { Db } from './db'

export const SCRIPT_PREFIX = 'generic:'

export type EnableResult =
  | { ok: true; domain: string }
  | { ok: false; reason: 'invalid' | 'permission_denied' | 'already_enabled' | 'already_tracked' }

export interface DomainDeps {
  db: Db
}

export class DomainService {
  constructor(private readonly deps: DomainDeps) {}

  async lists(): Promise<DomainLists> {
    const { db } = this.deps
    return { enabled: (await db.getKv('enabled_domains')) ?? [], denied: (await db.getKv('denied_domains')) ?? [] }
  }

  // enable must run from a click handler: the permission prompt is the first awaited call,
  // because Chrome grants permissions.request only inside the user gesture.
  async enable(input: string): Promise<EnableResult> {
    const domain = normalizeDomain(input)
    if (!domain) return { ok: false, reason: 'invalid' }
    // Medium hosts are tracked without enabling. Checked synchronously, before the prompt.
    if (chooseAdapter(domain, [], { enabled: [], denied: [] }) === 'freedium-medium') return { ok: false, reason: 'already_tracked' }
    const origins = originPatterns(domain)
    if (!(await chrome.permissions.request({ origins }))) return { ok: false, reason: 'permission_denied' }
    const { enabled } = await this.lists()
    if (enabled.includes(domain)) return { ok: false, reason: 'already_enabled' }
    await registerScript(SCRIPT_PREFIX + domain, origins)
    await this.deps.db.setKv('enabled_domains', [...enabled, domain].sort())
    return { ok: true, domain }
  }

  async disable(domain: string): Promise<void> {
    await unregisterScripts([SCRIPT_PREFIX + domain])
    try {
      await chrome.permissions.remove({ origins: originPatterns(domain) })
    } catch {
      // The permission may already be gone; the list entry must be removed regardless.
    }
    const { enabled } = await this.lists()
    await this.deps.db.setKv('enabled_domains', enabled.filter((d) => d !== domain))
  }

  async deny(input: string): Promise<{ ok: true; domain: string } | { ok: false; reason: 'invalid' }> {
    const domain = normalizeDomain(input)
    if (!domain) return { ok: false, reason: 'invalid' }
    const { denied } = await this.lists()
    if (!denied.includes(domain)) await this.deps.db.setKv('denied_domains', [...denied, domain].sort())
    return { ok: true, domain }
  }

  async undeny(domain: string): Promise<void> {
    const { denied } = await this.lists()
    await this.deps.db.setKv('denied_domains', denied.filter((d) => d !== domain))
  }

  // reconcile makes registered generic content scripts match enabled_domains.
  async reconcile(): Promise<void> {
    const { enabled } = await this.lists()
    const registered = (await registeredIds()).filter((id) => id.startsWith(SCRIPT_PREFIX))
    const wanted = new Set(enabled.map((d) => SCRIPT_PREFIX + d))
    const stale = registered.filter((id) => !wanted.has(id))
    if (stale.length > 0) await unregisterScripts(stale)
    for (const d of enabled) {
      if (!registered.includes(SCRIPT_PREFIX + d)) await registerScript(SCRIPT_PREFIX + d, originPatterns(d))
    }
  }

  async plan(host: string, config: RemoteConfig): Promise<AdapterName | null> {
    return chooseAdapter(host, config.freediumHosts, await this.lists())
  }
}

async function registerScript(id: string, matches: string[]): Promise<void> {
  const script = { id, matches, js: ['content.js'], runAt: 'document_idle' as const, persistAcrossSessions: true }
  if ((await registeredIds()).includes(id)) await chrome.scripting.updateContentScripts([script])
  else await chrome.scripting.registerContentScripts([script])
}

// unregisterScripts passes only ids Chrome knows; it throws on an unknown id.
async function unregisterScripts(ids: string[]): Promise<void> {
  const known = new Set(await registeredIds())
  const present = ids.filter((id) => known.has(id))
  if (present.length > 0) await chrome.scripting.unregisterContentScripts({ ids: present })
}

async function registeredIds(): Promise<string[]> {
  return (await chrome.scripting.getRegisteredContentScripts()).map((s) => s.id)
}
