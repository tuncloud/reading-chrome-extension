import { expect, test, vi } from 'vitest'
import { consoleLog } from './log'

test('consoleLog prefixes the scope', () => {
  const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
  consoleLog('background').warn('sync failed', 1)
  expect(warn).toHaveBeenCalledWith('[reading-tracker:background] sync failed', 1)
})
