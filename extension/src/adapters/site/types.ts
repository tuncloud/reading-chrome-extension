import type { RemoteConfig } from '../../domain/config'
import type { AdapterName } from '../../domain/domains'
import type { ArticleMeta } from '../../domain/reading'

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
