// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'
import { FakeContentScripts, FakePermissions, MemoryStore } from '../../../test/fakes'
import { DomainService } from '../../application/domains'
import type { SessionState } from '../../application/session'
import { OptionsApp } from './OptionsApp'
import type { OptionsServices } from './options-controller'

afterEach(cleanup)

function services(): OptionsServices & { refreshed: () => number } {
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
    domains: new DomainService({ store: new MemoryStore(), permissions: new FakePermissions(), scripts: new FakeContentScripts() }),
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
