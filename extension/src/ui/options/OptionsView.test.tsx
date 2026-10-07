// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { INSTALLATION } from '../../../test/helpers'
import { OptionsView, type OptionsActions, type OptionsState } from './OptionsView'

afterEach(cleanup)

describe('<OptionsView>', () => {
  let actions: OptionsActions

  beforeEach(() => {
    actions = {
      connect: vi.fn(), disconnect: vi.fn(), deleteAccount: vi.fn(), searchPages: vi.fn(), choosePage: vi.fn(),
      enableDomain: vi.fn(), disableDomain: vi.fn(), denyDomain: vi.fn(), undenyDomain: vi.fn(),
    }
  })

  const state = (over: Partial<OptionsState> = {}): OptionsState => ({
    session: { kind: 'connected', user: { id: 'u1', name: 'Tuan' }, installation: INSTALLATION },
    domains: { enabled: [], denied: [] }, pages: null, busy: false, note: null, ...over,
  })
  const view = (s: OptionsState) => render(<OptionsView state={s} actions={actions} />).container
  const submit = (container: HTMLElement, form: string, value: string) => {
    const f = container.querySelector<HTMLFormElement>(`form[data-form=${form}]`)!
    fireEvent.change(within(f).getByRole('textbox'), { target: { value } })
    fireEvent.submit(f)
  }

  test('disconnected: only Connect in the account section, no setup', () => {
    const c = view(state({ session: { kind: 'disconnected' } }))
    fireEvent.click(screen.getByRole('button', { name: 'Connect Notion' }))
    expect(actions.connect).toHaveBeenCalled()
    expect(screen.queryByRole('button', { name: 'Disconnect' })).toBeNull()
    expect(c.querySelector('[data-section=setup]')).toBeNull()
  })

  test('active: workspace shown, disconnect and delete wired, no setup', () => {
    const c = view(state())
    expect(c.textContent).toContain("Tuan's Notion")
    fireEvent.click(screen.getByRole('button', { name: 'Disconnect' }))
    fireEvent.click(screen.getByRole('button', { name: 'Delete account' }))
    expect(actions.disconnect).toHaveBeenCalled()
    expect(actions.deleteAccount).toHaveBeenCalled()
    expect(c.querySelector('[data-section=setup]')).toBeNull()
  })

  test('revoked: Reconnect', () => {
    view(state({ session: { kind: 'connected', user: null, installation: { ...INSTALLATION, status: 'revoked' } } }))
    fireEvent.click(screen.getByRole('button', { name: 'Reconnect' }))
    expect(actions.connect).toHaveBeenCalled()
  })

  test('T31: needs_setup shows the page picker', () => {
    const needsSetup = { ...INSTALLATION, status: 'needs_setup', setupError: 'template_not_found', libraryUrl: null }
    const c = view(state({
      session: { kind: 'connected', user: null, installation: needsSetup },
      pages: [{ id: 'p1', title: 'Reading', url: 'https://www.notion.so/p1' }],
    }))
    expect(c.querySelector('[data-section=setup]')?.textContent).toContain('template_not_found')
    submit(c, 'search', 'Read')
    expect(actions.searchPages).toHaveBeenCalledWith('Read')
    fireEvent.click(screen.getByRole('button', { name: 'Use this page' }))
    expect(actions.choosePage).toHaveBeenCalledWith('p1')
  })

  test('an empty search result says so', () => {
    view(state({ session: { kind: 'connected', user: null, installation: { ...INSTALLATION, status: 'needs_setup' } }, pages: [] }))
    expect(screen.getByText(/No pages found/)).toBeTruthy()
  })

  test('T31: tracked and denied domains', () => {
    const c = view(state({ domains: { enabled: ['example.com'], denied: ['spam.net'] } }))
    submit(c, 'enable', 'blog.org')
    submit(c, 'deny', 'ads.com')
    expect(actions.enableDomain).toHaveBeenCalledWith('blog.org')
    expect(actions.denyDomain).toHaveBeenCalledWith('ads.com')
    fireEvent.click(c.querySelector<HTMLButtonElement>('[data-list=enabled] button')!)
    fireEvent.click(c.querySelector<HTMLButtonElement>('[data-list=denied] button')!)
    expect(actions.disableDomain).toHaveBeenCalledWith('example.com')
    expect(actions.undenyDomain).toHaveBeenCalledWith('spam.net')
    expect(c.textContent).toContain('Medium and Freedium are always tracked')
  })

  test('busy disables buttons; the note is announced', () => {
    const c = view(state({ busy: true, note: 'Tracking blog.org.' }))
    expect([...c.querySelectorAll('button')].every((b) => b.disabled)).toBe(true)
    expect(screen.getByRole('status').textContent).toBe('Tracking blog.org.')
  })
})
