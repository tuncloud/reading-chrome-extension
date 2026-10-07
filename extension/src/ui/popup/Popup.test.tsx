// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import type { PopupModel } from '../../application/popup-model'
import { connectMessage } from './messages'
import { Popup, type PopupActions } from './Popup'

afterEach(cleanup)

describe('<Popup>', () => {
  let actions: PopupActions

  beforeEach(() => {
    actions = { connect: vi.fn(), openOptions: vi.fn(), openUrl: vi.fn() }
  })

  test.each([
    [{ kind: 'disconnected' }, 'Connect Notion', 'connect'],
    [{ kind: 'needs_setup', workspaceName: 'W', setupError: 'template_not_found' }, 'Finish setup', 'openOptions'],
    [{ kind: 'revoked', workspaceName: 'W' }, 'Reconnect', 'connect'],
  ] as const)('T30: %j shows "%s"', (model, label, action) => {
    render(<Popup model={model as PopupModel} actions={actions} />)
    fireEvent.click(screen.getByRole('button', { name: label }))
    expect(actions[action as keyof PopupActions]).toHaveBeenCalledTimes(1)
  })

  test('needs_setup explains the setup error', () => {
    render(<Popup model={{ kind: 'needs_setup', workspaceName: 'W', setupError: 'database_missing:Daily Log' }} actions={actions} />)
    expect(screen.getByText(/database_missing:Daily Log/)).toBeTruthy()
  })

  const active: PopupModel = {
    kind: 'active', workspaceName: 'W', todayMinutes: 42, libraryUrl: 'https://www.notion.so/lib', unsynced: 3,
    current: { url: 'https://e.com/a', title: 'Current one', minutes: 7, progressPercent: 64, completed: true, notionUrl: 'https://www.notion.so/a' },
    recent: [
      { url: 'https://e.com/a', title: 'Current one', minutes: 7, progressPercent: 64, completed: true, notionUrl: 'https://www.notion.so/a' },
      { url: 'https://e.com/b', title: 'Older', minutes: 2, progressPercent: 10, completed: null, notionUrl: null },
    ],
  }

  test("active shows today's minutes, the current article and recent reads", () => {
    const { container } = render(<Popup model={active} actions={actions} />)
    expect(container.querySelector('[data-role=today]')?.textContent).toBe('42 min')
    const current = container.querySelector('[data-role=current]')!
    expect(current.textContent).toContain('Current one')
    expect(current.textContent).toContain('7 min · 64% · ✓')
    expect(container.querySelectorAll('[data-role=recent] li')).toHaveLength(2)
    expect(screen.getByText('3 segments waiting to sync')).toBeTruthy()
  })

  test('recent items open the Notion page when there is one, else the article', () => {
    const { container } = render(<Popup model={active} actions={actions} />)
    const links = container.querySelectorAll<HTMLButtonElement>('[data-role=recent] li button')
    fireEvent.click(links[0]!)
    fireEvent.click(links[1]!)
    expect(actions.openUrl).toHaveBeenNthCalledWith(1, 'https://www.notion.so/a')
    expect(actions.openUrl).toHaveBeenNthCalledWith(2, 'https://e.com/b')
  })

  test('M5: only https URLs are opened; others do nothing and the library button is hidden', () => {
    const bad = (url: string, notionUrl: string | null): PopupModel => ({
      ...active, libraryUrl: url, current: null,
      recent: [{ url: 'http://e.com/plain', title: 'Plain http', minutes: 1, progressPercent: 1, completed: null, notionUrl }],
    })
    for (const url of ['javascript:alert(1)', 'http://www.notion.so/x', 'file:///etc/passwd', 'not a url']) {
      const { unmount } = render(<Popup model={bad(url, url)} actions={actions} />)
      fireEvent.click(screen.getByRole('button', { name: 'Plain http' }))
      expect(actions.openUrl).not.toHaveBeenCalled()
      expect(screen.queryByRole('button', { name: 'Open library in Notion' })).toBeNull()
      unmount()
    }
    render(<Popup model={bad('https://www.notion.so/lib', null)} actions={actions} />)
    fireEvent.click(screen.getByRole('button', { name: 'Plain http' }))
    expect(actions.openUrl).not.toHaveBeenCalled() // the article's own http URL is refused too
    fireEvent.click(screen.getByRole('button', { name: 'Open library in Notion' }))
    expect(actions.openUrl).toHaveBeenCalledWith('https://www.notion.so/lib')
  })

  test('the library button opens library_url and is hidden without one', () => {
    const { rerender } = render(<Popup model={active} actions={actions} />)
    fireEvent.click(screen.getByRole('button', { name: 'Open library in Notion' }))
    expect(actions.openUrl).toHaveBeenCalledWith('https://www.notion.so/lib')
    rerender(<Popup model={{ ...active, libraryUrl: null, current: null, recent: [], unsynced: 0 }} actions={actions} />)
    expect(screen.queryByRole('button', { name: 'Open library in Notion' })).toBeNull()
    expect(screen.getByText(/Nothing read yet/)).toBeTruthy()
  })

  test('a note is announced', () => {
    render(<Popup model={{ kind: 'disconnected' }} actions={actions} note={connectMessage('access_denied')} />)
    expect(screen.getByRole('status').textContent).toBe('Notion access was not granted.')
  })

  test('page titles render as text, never HTML', () => {
    const evil = { ...active, recent: [{ ...active.recent[1]!, title: '<img src=x onerror=alert(1)>' }] }
    const { container } = render(<Popup model={evil} actions={actions} />)
    expect(container.querySelector('img')).toBeNull()
  })
})

test('connectMessage maps reasons', () => {
  expect(connectMessage('cancelled')).toBe('Connection cancelled.')
  expect(connectMessage('network_error')).toBe("Can't reach the server. Try again.")
  expect(connectMessage('invalid_grant')).toBe('Connection failed (invalid_grant). Try again.')
})
