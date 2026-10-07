// SessionService owns the device's connection to the backend (spec §2.1–§2.5, §2.17, T29).
import { isApiError, NetworkError, NotConnectedError } from '../shared/errors'
import type { Log } from '../shared/log'
import type { ConnectResult, SessionState } from '../shared/types'
import type { ApiClient } from './api'
import type { Db, KvKey } from './db'
import { deviceName } from './device'
import { challengeFor, randomVerifier } from './pkce'


export const AUTH_KEYS: readonly KvKey[] = ['device_token', 'device_id', 'user', 'installation']

export interface SessionDeps {
  db: Db
  api: Pick<ApiClient, 'oauthStartUrl' | 'exchangeCode' | 'me' | 'logout' | 'deleteAccount'>
  log: Log
  // afterConnect runs once a device token is stored (e.g. to push the waiting outbox).
  afterConnect?: () => void
}

export class SessionService {
  constructor(private readonly deps: SessionDeps) {}

  async state(): Promise<SessionState> {
    const { db } = this.deps
    if (!(await db.getKv('device_token'))) return { kind: 'disconnected' }
    return { kind: 'connected', user: (await db.getKv('user')) ?? null, installation: (await db.getKv('installation')) ?? null }
  }

  async connect(): Promise<ConnectResult> {
    const { api, db, log } = this.deps
    const verifier = randomVerifier()
    const challenge = await challengeFor(verifier)
    const url = api.oauthStartUrl({ extRedirect: chrome.identity.getRedirectURL(), codeChallenge: challenge })
    let redirect: string
    try {
      const r = await chrome.identity.launchWebAuthFlow({ url, interactive: true })
      if (!r) throw new Error('auth flow ended without a redirect')
      redirect = r
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
      const grant = await api.exchangeCode({ code, codeVerifier: verifier, deviceName: deviceName(navigator) })
      await db.setKv('device_token', grant.deviceToken)
      await db.setKv('device_id', grant.deviceId)
      await db.setKv('user', grant.user)
      await db.setKv('installation', grant.installation)
      await db.deleteKv('sync_failures', 'sync_backoff_until') // a fresh token must not wait out an old backoff
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
    const { db, api, log } = this.deps
    const token = await db.getKv('device_token')
    if (!token) return { kind: 'disconnected' }
    try {
      const me = await api.me(token)
      await db.setKv('user', me.user)
      await db.setKv('installation', me.installation)
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
    const token = await this.deps.db.getKv('device_token')
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
    const { db, api } = this.deps
    const token = await db.getKv('device_token')
    if (!token) throw new NotConnectedError()
    try {
      await api.deleteAccount(token)
    } catch (e) {
      if (!isApiError(e, 401)) throw e // 401: the server already revoked the token, the account is gone
    }
    await this.forget()
    await db.clearReading()
  }

  private async forget(): Promise<void> {
    await this.deps.db.deleteKv(...AUTH_KEYS)
  }
}
