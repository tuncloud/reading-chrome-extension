import type { AdapterName } from '../../shared/domains'
import { freediumMedium } from './freedium-medium'
import { generic } from './generic'
import type { SiteAdapter } from './types'

const ADAPTERS: Record<AdapterName, SiteAdapter> = { 'freedium-medium': freediumMedium, generic }

export function adapterFor(name: AdapterName): SiteAdapter {
  return ADAPTERS[name]
}
