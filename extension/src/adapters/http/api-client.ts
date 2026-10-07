// HttpApi is the backend over fetch (spec §2). It only moves JSON; decisions stay in use cases.
import { parseRemoteConfig, type RemoteConfig } from '../../domain/config'
import { ApiError, NetworkError } from '../../application/errors'
import type { Api } from '../../application/ports'
import type { ArticleStatus, Installation, Me, NotionPage, PushBody, PushResult, TokenGrant } from '../../application/types'
import * as wire from './wire'

export type Fetch = (url: string, init?: RequestInit) => Promise<Response>

export interface HttpApiOptions {
  baseUrl: string
  fetch?: Fetch
  timeoutMs?: number
}

interface RequestOptions {
  token?: string
  body?: unknown
}

async function apiError(res: Response): Promise<ApiError> {
  let code = res.status >= 500 ? 'internal' : `http_${res.status}`
  let message = res.statusText || `HTTP ${res.status}`
  try {
    const body = (await res.json()) as { error?: { code?: string; message?: string } }
    if (body?.error?.code) {
      code = body.error.code
      message = body.error.message ?? message
    }
  } catch {
    // not the §2.0 body; keep the status-derived code
  }
  return new ApiError(res.status, code, message)
}

export class HttpApi implements Api {
  private readonly base: string
  private readonly fetchFn: Fetch
  private readonly timeoutMs: number

  constructor(opts: HttpApiOptions) {
    this.base = opts.baseUrl.replace(/\/+$/, '')
    this.fetchFn = opts.fetch ?? ((url, init) => fetch(url, init))
    this.timeoutMs = opts.timeoutMs ?? 20_000
  }

  oauthStartUrl(p: { extRedirect: string; codeChallenge: string }): string {
    const q = new URLSearchParams({ ext_redirect: p.extRedirect, code_challenge: p.codeChallenge, code_challenge_method: 'S256' })
    return `${this.base}/v1/oauth/notion/start?${q.toString()}`
  }

  async config(): Promise<RemoteConfig> {
    return parseRemoteConfig(await this.request('GET', '/v1/config'))
  }

  async exchangeCode(p: { code: string; codeVerifier: string; deviceName: string }): Promise<TokenGrant> {
    const body = { code: p.code, code_verifier: p.codeVerifier, device_name: p.deviceName }
    return wire.tokenGrant(await this.request<wire.WireToken>('POST', '/v1/auth/token', { body }))
  }

  async me(token: string): Promise<Me> {
    return wire.me(await this.request<wire.WireMe>('GET', '/v1/me', { token }))
  }

  async logout(token: string): Promise<void> {
    await this.request('POST', '/v1/auth/logout', { token })
  }

  async deleteAccount(token: string): Promise<void> {
    await this.request('DELETE', '/v1/me', { token })
  }

  async push(token: string, body: PushBody): Promise<PushResult> {
    return wire.pushResult(await this.request<wire.WirePushResult>('POST', '/v1/sync/push', { token, body: wire.pushBody(body) }))
  }

  async articlesStatus(token: string, urls: readonly string[]): Promise<Record<string, ArticleStatus>> {
    const res = await this.request<{ items: Record<string, wire.WireArticleStatus> | null }>(
      'POST', '/v1/articles/status', { token, body: { canonical_urls: urls } },
    )
    return Object.fromEntries(Object.entries(res.items ?? {}).map(([url, s]) => [url, wire.articleStatus(s)]))
  }

  async setupPages(token: string, query: string): Promise<NotionPage[]> {
    const q = new URLSearchParams({ q: query })
    const res = await this.request<{ pages: NotionPage[] | null }>('GET', `/v1/setup/pages?${q.toString()}`, { token })
    return (res.pages ?? []).map((p) => ({ id: p.id, title: p.title, url: p.url }))
  }

  async setup(token: string, parentPageId: string): Promise<Installation> {
    const res = await this.request<wire.WireInstallation>('POST', '/v1/setup', { token, body: { parent_page_id: parentPageId } })
    const inst = wire.installation(res)
    if (!inst) throw new ApiError(500, 'bad_response', 'POST /v1/setup returned no installation')
    return inst
  }

  private async request<T = unknown>(method: string, path: string, opts: RequestOptions = {}): Promise<T> {
    const headers: Record<string, string> = { accept: 'application/json' }
    if (opts.token) headers.authorization = `Bearer ${opts.token}`
    if (opts.body !== undefined) headers['content-type'] = 'application/json'
    let res: Response
    try {
      res = await this.fetchFn(this.base + path, {
        method,
        headers,
        body: opts.body === undefined ? undefined : JSON.stringify(opts.body),
        signal: AbortSignal.timeout(this.timeoutMs),
      })
    } catch (e) {
      throw new NetworkError(`${method} ${path.split('?')[0]}: ${e instanceof Error ? e.message : String(e)}`)
    }
    if (!res.ok) throw await apiError(res)
    if (res.status === 202 || res.status === 204) return undefined as T
    let text: string
    try {
      text = await res.text()
    } catch (e) {
      throw new NetworkError(`${method} ${path.split('?')[0]}: ${e instanceof Error ? e.message : String(e)}`)
    }
    if (!text) return undefined as T
    try {
      return JSON.parse(text) as T
    } catch {
      throw new ApiError(res.status, 'bad_response', `${method} ${path.split('?')[0]}: response is not JSON`)
    }
  }
}
