// The options page's state machine: run an action, keep the page busy meanwhile,
// then re-read what changed and re-render with a short note.
import { isApiError } from '../shared/errors'
import type { DomainService } from '../background/domains'
import type { ConnectResult, SessionService } from '../background/session'
import type { SetupService } from '../background/setup'
import { connectMessage } from '../popup/messages'
import type { OptionsActions, OptionsState } from './OptionsView'

export interface OptionsServices {
  session: Pick<SessionService, 'state' | 'refresh' | 'logout' | 'deleteAccount'>
  setup: Pick<SetupService, 'searchPages' | 'createDatabases'>
  domains: Pick<DomainService, 'lists' | 'enable' | 'disable' | 'deny' | 'undeny'>
  connect: () => Promise<ConnectResult>
}

export interface OptionsUi {
  render(state: OptionsState, actions: OptionsActions): void
  confirm(message: string): boolean
}

const ENABLE_FAILURES = {
  invalid: 'Enter a domain like example.com.',
  permission_denied: 'Chrome permission was not granted.',
  already_enabled: 'That site is already tracked.',
  already_tracked: 'Medium and Freedium are always tracked.',
} as const

function errorText(e: unknown): string {
  return isApiError(e) ? e.message : e instanceof Error ? e.message : String(e)
}

export function createOptionsController(s: OptionsServices, ui: OptionsUi) {
  let state: OptionsState = {
    session: { kind: 'disconnected' }, domains: { enabled: [], denied: [] }, pages: null, busy: false, note: null,
  }
  const draw = () => ui.render(state, actions)

  // run marks the page busy, runs the action (whose first await is the action's own), then refreshes.
  async function run(action: () => Promise<string | null>): Promise<void> {
    const pending = action()
    state = { ...state, busy: true, note: null }
    draw()
    let note: string | null
    try {
      note = await pending
    } catch (e) {
      note = errorText(e)
    }
    state = { ...state, session: await s.session.state(), domains: await s.domains.lists(), busy: false, note }
    draw()
  }

  const actions: OptionsActions = {
    connect: () => run(async () => {
      const res = await s.connect()
      return res.ok ? 'Connected.' : connectMessage(res.reason)
    }),
    disconnect: () => run(async () => {
      await s.session.logout()
      return 'Disconnected.'
    }),
    deleteAccount: () => {
      const message = 'Delete your Reading Tracker account? Your Notion pages stay; reading data on this device is erased.'
      if (!ui.confirm(message)) return Promise.resolve()
      return run(async () => {
        try {
          await s.session.deleteAccount()
          return 'Account deleted.'
        } catch {
          return 'Could not delete the account. Try again.'
        }
      })
    },
    searchPages: (query) => run(async () => {
      const pages = await s.setup.searchPages(query)
      state = { ...state, pages }
      return null
    }),
    choosePage: (pageId) => run(async () => {
      await s.setup.createDatabases(pageId)
      state = { ...state, pages: null }
      return 'Reading Library and Daily Log created.'
    }),
    enableDomain: (input) => run(async () => {
      const res = await s.domains.enable(input) // first await: keeps the permission prompt in the gesture
      return res.ok ? `Tracking ${res.domain}.` : ENABLE_FAILURES[res.reason]
    }),
    disableDomain: (domain) => run(async () => {
      await s.domains.disable(domain)
      return `Stopped tracking ${domain}.`
    }),
    denyDomain: (input) => run(async () => {
      const res = await s.domains.deny(input)
      return res.ok ? `${res.domain} will never be tracked.` : ENABLE_FAILURES.invalid
    }),
    undenyDomain: (domain) => run(async () => {
      await s.domains.undeny(domain)
      return `${domain} removed from the block list.`
    }),
  }

  return {
    actions,
    state: () => state,
    async load(): Promise<void> {
      state = { ...state, session: await s.session.state(), domains: await s.domains.lists() }
      draw()
    },
  }
}
