import { afterEach, expect, test, vi } from 'vitest'
import { activeTabId } from './tabs'

afterEach(() => vi.unstubAllGlobals())

test('activeTabId returns the id or null', async () => {
  const tabs = { query: vi.fn(async () => [{ id: 7 }]) }
  vi.stubGlobal('chrome', { tabs })
  expect(await activeTabId()).toBe(7)
  tabs.query.mockResolvedValueOnce([])
  expect(await activeTabId()).toBeNull()
})
