// RemoteConfig is GET /v1/config (spec §2.10) in camelCase. Every tracking threshold the
// extension uses comes from here; none is inlined in tracker or sync code (spec §7).

export interface TrackingConfig {
  idleSeconds: number
  segmentSeconds: number
  minCreateSeconds: number
  completeProgress: number
  completeTimeRatio: number
  wordsPerMinute: number
  syncIntervalMinutes: number
  syncThresholdSegments: number
}

export interface RemoteConfig {
  version: number
  tracking: TrackingConfig
  freediumHosts: string[]
  stripQueryParams: string[]
}

export class ConfigError extends Error {
  override name = 'ConfigError'
}

const TRACKING_FIELDS: readonly [keyof TrackingConfig, string][] = [
  ['idleSeconds', 'idle_seconds'],
  ['segmentSeconds', 'segment_seconds'],
  ['minCreateSeconds', 'min_create_seconds'],
  ['completeProgress', 'complete_progress'],
  ['completeTimeRatio', 'complete_time_ratio'],
  ['wordsPerMinute', 'words_per_minute'],
  ['syncIntervalMinutes', 'sync_interval_minutes'],
  ['syncThresholdSegments', 'sync_threshold_segments'],
]

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

function stringList(v: unknown, field: string): string[] {
  if (!Array.isArray(v) || !v.every((x) => typeof x === 'string')) {
    throw new ConfigError(`config: ${field} must be a list of strings`)
  }
  return [...v]
}

export function parseRemoteConfig(raw: unknown): RemoteConfig {
  if (!isRecord(raw) || !isRecord(raw.tracking)) throw new ConfigError('config: missing tracking')
  const wire = raw.tracking
  const tracking = {} as TrackingConfig
  for (const [key, field] of TRACKING_FIELDS) {
    const v = wire[field]
    if (typeof v !== 'number' || !Number.isFinite(v) || v <= 0) {
      throw new ConfigError(`config: tracking.${field} must be a positive number`)
    }
    tracking[key] = v
  }
  return {
    version: typeof raw.version === 'number' ? raw.version : 0,
    tracking,
    freediumHosts: stringList(raw.freedium_hosts, 'freedium_hosts').map((h) => h.toLowerCase()),
    stripQueryParams: stringList(raw.strip_query_params, 'strip_query_params'),
  }
}
