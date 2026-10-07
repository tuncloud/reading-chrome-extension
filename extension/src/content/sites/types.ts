import type { RemoteConfig } from '../../shared/config'
import type { AdapterName } from '../../shared/domains'
import type { ArticleMeta } from '../../shared/reading'

export interface PageContext {
  doc: Document
  // location is the page URL as the browser shows it.
  location: string
  config: RemoteConfig
}

// SiteAdapter reads an article's identity and metadata from a page (spec T26).
export interface SiteAdapter {
  name: AdapterName
  extract(ctx: PageContext): ArticleMeta | null
  contentRoot(doc: Document): Element
}
