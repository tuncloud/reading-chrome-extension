// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'
import { installChrome } from '../../../test/chrome'
import { testDb } from '../../../test/helpers'
import { DomainService } from '../../background/domains'
import type { SessionState } from '../../background/session'
import { OptionsApp } from './OptionsApp'
import type { OptionsServices } from './options-controller'

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

function services(): OptionsServices & { refreshed: () => number } {
  installChrome()
  installChrome()
  let refreshes = 0
  const session: SessionState = { kind: 'disconnected' }
  return {
    session: {
      state: async () => session,
      refresh: async () => {
        refreshes++
        return session
      },
      logout: async () => {},
      deleteAccount: async () => {},
    },
    setup: { searchPages: async () => [], createDatabases: vi.fn() },
    domains: new DomainService({ db: testDb() }),
    connect: async () => ({ ok: false, reason: 'cancelled' }),
    refreshed: () => refreshes,
  }
}

test('loads, refreshes /v1/me once, and renders the page', async () => {
  const s = services()
  render(<OptionsApp services={s} confirm={() => false} />)
  expect(await screen.findByRole('button', { name: 'Connect Notion' })).toBeTruthy()
  await vi.waitFor(() => expect(s.refreshed()).toBe(1))
})

test('actions re-render through React state', async () => {
  render(<OptionsApp services={services()} confirm={() => false} />)
  const input = (await screen.findAllByPlaceholderText('example.com'))[0]!
  fireEvent.change(input, { target: { value: 'example.com' } })
  fireEvent.submit(input.closest('form')!)
  expect(await screen.findByText('Tracking example.com.')).toBeTruthy()
  expect(screen.getByText('example.com', { selector: 'li span' })).toBeTruthy()
})
