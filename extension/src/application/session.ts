// SessionService owns the device's connection to the backend (spec §2.1–§2.5, §2.17, T29).
import { isApiError, NetworkError, NotConnectedError } from './errors'
import type { Api, AuthFlow, KvKey, LocalStore, Log, Pkce } from './ports'
import type { Installation, User } from './types'

export type SessionState =
  | { kind: 'disconnected' }
  | { kind: 'connected'; user: User | null; installation: Installation | null }

export type ConnectResult = { ok: true; installation: Installation | null } | { ok: false; reason: string }

export const AUTH_KEYS: readonly KvKey[] = ['device_token', 'device_id', 'user', 'installation']

export interface SessionDeps {
  store: LocalStore
  api: Api
  authFlow: AuthFlow
  pkce: Pkce
  deviceName: () => string
  log: Log
  // afterConnect runs once a device token is stored (e.g. to push the waiting outbox).
  afterConnect?: () => void
}

export class SessionService {
  constructor(private readonly deps: SessionDeps) {}

  async state(): Promise<SessionState> {
    const { store } = this.deps
    if (!(await store.getKv('device_token'))) return { kind: 'disconnected' }
    return { kind: 'connected', user: (await store.getKv('user')) ?? null, installation: (await store.getKv('installation')) ?? null }
  }

  async connect(): Promise<ConnectResult> {
    const { api, authFlow, pkce, store, log } = this.deps
    const { verifier, challenge } = await pkce.create()
    const url = api.oauthStartUrl({ extRedirect: authFlow.redirectUrl(), codeChallenge: challenge })
    let redirect: string
    try {
      redirect = await authFlow.launch(url)
    } catch (e) {
      log.info(`connect: auth window closed (${e instanceof Error ? e.message : String(e)})`)
      return { ok: false, reason: 'cancelled' }
    }
    let params: URLSearchParams
    try {
      params = new URL(redirect).searchParams
    } catch {
      return { ok: false, reason: 'invalid_response' }
    }
    const error = params.get('error')
    if (error) return { ok: false, reason: error }
    const code = params.get('code')
    if (!code) return { ok: false, reason: 'invalid_response' }
    try {
      const grant = await api.exchangeCode({ code, codeVerifier: verifier, deviceName: this.deps.deviceName() })
      await store.setKv('device_token', grant.deviceToken)
      await store.setKv('device_id', grant.deviceId)
      await store.setKv('user', grant.user)
      await store.setKv('installation', grant.installation)
      const after = await this.refresh() // T29: GET /v1/me after the exchange; best-effort, it never throws
      if (after.kind === 'disconnected') return { ok: false, reason: 'unauthorized' }
      this.deps.afterConnect?.()
      return { ok: true, installation: grant.installation }
    } catch (e) {
      if (isApiError(e)) return { ok: false, reason: e.code }
      if (e instanceof NetworkError) return { ok: false, reason: 'network_error' }
      throw e
    }
  }

  // refresh reloads GET /v1/me; offline it returns the cached state.
  async refresh(): Promise<SessionState> {
    const { store, api, log } = this.deps
    const token = await store.getKv('device_token')
    if (!token) return { kind: 'disconnected' }
    try {
      const me = await api.me(token)
      await store.setKv('user', me.user)
      await store.setKv('installation', me.installation)
      return { kind: 'connected', user: me.user, installation: me.installation }
    } catch (e) {
      if (isApiError(e, 401)) {
        await this.forget()
        return { kind: 'disconnected' }
      }
      if (!(e instanceof NetworkError)) {
        log.warn(`session: refresh failed (${e instanceof Error ? e.message : String(e)})`)
      }
      return this.state()
    }
  }

  // logout revokes the device token on the server and always disconnects locally.
  async logout(): Promise<void> {
    const token = await this.deps.store.getKv('device_token')
    if (token) {
      try {
        await this.deps.api.logout(token)
      } catch (e) {
        this.deps.log.warn(`logout: server call failed (${e instanceof Error ? e.message : String(e)})`)
      }
    }
    await this.forget()
  }

  // deleteAccount asks the server to purge the account (§2.17), then clears local data.
  async deleteAccount(): Promise<void> {
    const { store, api } = this.deps
    const token = await store.getKv('device_token')
    if (!token) throw new NotConnectedError()
    await api.deleteAccount(token)
    await this.forget()
    await store.clearReading()
  }

  private async forget(): Promise<void> {
    await this.deps.store.deleteKv(...AUTH_KEYS)
  }
}
