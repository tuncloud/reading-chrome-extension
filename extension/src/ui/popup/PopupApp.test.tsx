// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'
import type { PopupModel } from '../../application/popup-model'
import type { ConnectResult } from '../../background/session'
import { PopupApp, type PopupAppProps } from './PopupApp'

afterEach(cleanup)

function props(over: Partial<PopupAppProps> = {}): PopupAppProps {
  return {
    load: vi.fn(async (): Promise<PopupModel> => ({ kind: 'disconnected' })),
    refresh: vi.fn(async () => {}),
    connect: vi.fn(async (): Promise<ConnectResult> => ({ ok: false, reason: 'cancelled' })),
    openOptions: vi.fn(),
    openUrl: vi.fn(),
    ...over,
  }
}

test('loads the model, refreshes /v1/me, then reloads', async () => {
  const p = props()
  render(<PopupApp {...p} />)
  expect(await screen.findByRole('button', { name: 'Connect Notion' })).toBeTruthy()
  await vi.waitFor(() => expect(p.load).toHaveBeenCalledTimes(2))
  expect(p.refresh).toHaveBeenCalledTimes(1)
})

test('a failed connect shows why and reloads', async () => {
  const p = props()
  render(<PopupApp {...p} />)
  fireEvent.click(await screen.findByRole('button', { name: 'Connect Notion' }))
  expect((await screen.findByRole('status')).textContent).toBe('Connection cancelled.')
  await vi.waitFor(() => expect(p.load).toHaveBeenCalledTimes(3))
})

test('a successful connect shows the new state', async () => {
  let connected = false
  const p = props({
    load: vi.fn(async (): Promise<PopupModel> => connected
      ? { kind: 'needs_setup', workspaceName: 'W', setupError: '' }
      : { kind: 'disconnected' }),
    connect: vi.fn(async (): Promise<ConnectResult> => {
      connected = true
      return { ok: true, installation: null }
    }),
  })
  render(<PopupApp {...p} />)
  fireEvent.click(await screen.findByRole('button', { name: 'Connect Notion' }))
  expect(await screen.findByRole('button', { name: 'Finish setup' })).toBeTruthy()
  expect(screen.queryByRole('status')).toBeNull()
})

test('a load failure is reported instead of a blank popup', async () => {
  render(<PopupApp {...props({ load: vi.fn(async () => { throw new Error('db closed') }) })} />)
  expect((await screen.findByRole('status')).textContent).toBe("Couldn't load your reading. Close and reopen the popup.")
})
