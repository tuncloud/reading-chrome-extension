import { afterEach, expect, test, vi } from 'vitest'
import { installChrome } from '../../test/chrome'
import { createServices } from './services'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.unstubAllEnvs()
})

test('createServices registers no chrome listeners and creates no alarms', () => {
  const ch = installChrome()
  vi.stubEnv('VITE_API_BASE', 'http://localhost:8080')
  createServices('test')
  expect(Object.values(ch.listeners).flat()).toHaveLength(0)
  expect(ch.chrome.alarms.create).not.toHaveBeenCalled()
})
