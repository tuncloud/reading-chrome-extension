// Messages between the content script / popup and the background service worker.
import type { RemoteConfig } from '../domain/config'
import type { AdapterName } from '../domain/domains'
import type { ArticleMeta, Segment } from '../domain/reading'
import type { ConnectResult } from './session'
import type { SyncOutcome } from './sync'

export interface TrackingPlan {
  adapter: AdapterName
  config: RemoteConfig
}

export type BackgroundRequest =
  | { type: 'tracking-plan'; host: string }
  | { type: 'record'; article: ArticleMeta; segment: Segment }
  | { type: 'connect' }
  | { type: 'sync-now' }

export type BackgroundReply<T extends BackgroundRequest['type']> =
  T extends 'tracking-plan' ? TrackingPlan | null
    : T extends 'record' ? { stored: boolean }
      : T extends 'connect' ? ConnectResult
        : T extends 'sync-now' ? SyncOutcome
          : never

const TYPES = new Set(['tracking-plan', 'record', 'connect', 'sync-now'])

export function isBackgroundRequest(m: unknown): m is BackgroundRequest {
  return typeof m === 'object' && m !== null && TYPES.has((m as { type?: unknown }).type as string)
}

// TabRequest goes from the popup to the content script of the active tab.
export type TabRequest = { type: 'current-page' }

export interface CurrentPage {
  canonicalUrl: string | null
}
