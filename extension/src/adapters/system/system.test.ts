import { expect, test, vi } from 'vitest'
import { consoleLog, SystemClock } from './system'

test('SystemClock reports now and an IANA time zone', () => {
  const c = new SystemClock()
  expect(Math.abs(c.now().getTime() - Date.now())).toBeLessThan(1000)
  expect(() => new Intl.DateTimeFormat('en', { timeZone: c.timeZone() })).not.toThrow()
})

test('consoleLog prefixes the scope', () => {
  const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
  consoleLog('background').warn('sync failed', 1)
  expect(warn).toHaveBeenCalledWith('[reading-tracker:background] sync failed', 1)
})
