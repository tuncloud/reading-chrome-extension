import { describe, expect, test } from 'vitest'
import { deviceName } from './device'

describe('deviceName', () => {
  test.each([
    [{ userAgentData: { platform: 'macOS' }, userAgent: '' }, 'Chrome on macOS'],
    [{ userAgent: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/130' }, 'Chrome on Windows'],
    [{ userAgent: 'Mozilla/5.0 (X11; Linux x86_64) Chrome/130' }, 'Chrome on Linux'],
    [{ userAgent: 'Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) Chrome/130' }, 'Chrome on ChromeOS'],
    [{ userAgent: 'something odd' }, 'Chrome'],
  ])('%j → %s', (nav, want) => {
    expect(deviceName(nav as unknown as Navigator)).toBe(want)
  })
})
