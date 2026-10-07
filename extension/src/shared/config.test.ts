import { describe, expect, test } from 'vitest'
import { wireConfig } from '../../test/fixtures'
import { ConfigError, parseRemoteConfig } from './config'

describe('parseRemoteConfig', () => {
  test('maps the §2.10 body to camelCase', () => {
    const c = parseRemoteConfig(wireConfig)
    expect(c.version).toBe(1)
    expect(c.tracking).toEqual({
      idleSeconds: 60, segmentSeconds: 60, minCreateSeconds: 30, completeProgress: 0.9,
      completeTimeRatio: 0.3, wordsPerMinute: 230, syncIntervalMinutes: 2, syncThresholdSegments: 20,
    })
    expect(c.freediumHosts).toEqual(['freedium-mirror.cfd', 'freedium.cfd'])
    expect(c.stripQueryParams).toContain('utm_*')
  })

  test.each([
    ['not an object', null],
    ['no tracking', { ...wireConfig, tracking: undefined }],
    ['zero idle', { ...wireConfig, tracking: { ...wireConfig.tracking, idle_seconds: 0 } }],
    ['string number', { ...wireConfig, tracking: { ...wireConfig.tracking, segment_seconds: '60' } }],
    ['hosts not strings', { ...wireConfig, freedium_hosts: [1] }],
    ['missing strip list', { ...wireConfig, strip_query_params: undefined }],
  ])('rejects %s', (_, raw) => {
    expect(() => parseRemoteConfig(raw)).toThrow(ConfigError)
  })
})
