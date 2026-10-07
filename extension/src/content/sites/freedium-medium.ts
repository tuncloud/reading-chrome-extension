// The Freedium/Medium adapter (spec T26): Medium posts and their Freedium mirrors.
import { mediumCanonical } from '../../shared/medium'
import { canonicalFromDocument, countWords, defaultContentRoot, finishMeta, firstText, metaContent } from './metadata'
import type { SiteAdapter } from './types'

export const freediumMedium: SiteAdapter = {
  name: 'freedium-medium',
  contentRoot: defaultContentRoot,
  extract({ doc, location, config }) {
    const canonicalUrl = mediumCanonical(location, config.freediumHosts) ?? canonicalFromDocument(doc, location, config)
    if (!canonicalUrl) return null
    return finishMeta({
      canonicalUrl,
      source: 'medium',
      title: firstText(doc, 'h1') || metaContent(doc, 'meta[property="og:title"]') || doc.title,
      author: metaContent(doc, 'meta[name="author"]') || firstText(doc, 'a[rel="author"], [data-testid="authorName"]'),
      siteName: metaContent(doc, 'meta[property="og:site_name"]'),
      wordCount: countWords(defaultContentRoot(doc)),
      publishedAt: metaContent(doc, 'meta[property="article:published_time"]'),
      originalUrl: location,
    })
  },
}
