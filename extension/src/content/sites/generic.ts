// The generic adapter (spec T26): any user-enabled domain.
import { canonicalFromDocument, countWords, defaultContentRoot, finishMeta, jsonLdArticle, metaContent } from './metadata'
import type { SiteAdapter } from './types'

export const generic: SiteAdapter = {
  name: 'generic',
  contentRoot: defaultContentRoot,
  extract({ doc, location, config }) {
    const canonicalUrl = canonicalFromDocument(doc, location, config)
    if (!canonicalUrl) return null
    const ld = jsonLdArticle(doc)
    return finishMeta({
      canonicalUrl,
      source: 'web',
      title: ld.headline || metaContent(doc, 'meta[property="og:title"]') || doc.title,
      author: ld.author || metaContent(doc, 'meta[name="author"]'),
      siteName: metaContent(doc, 'meta[property="og:site_name"]') || new URL(canonicalUrl).hostname.replace(/^www\./, ''),
      wordCount: ld.wordCount || countWords(defaultContentRoot(doc)),
      publishedAt: ld.datePublished || metaContent(doc, 'meta[property="article:published_time"]'),
      originalUrl: location,
    })
  },
}
