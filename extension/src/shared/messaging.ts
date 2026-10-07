// chrome.runtime / chrome.tabs messaging with a { ok, value | error } reply envelope.
import type { Log } from './log'
import type { BackgroundReply, BackgroundRequest, CurrentPage, TabRequest } from './messages'

export type Reply = { ok: true; value: unknown } | { ok: false; error: string }

// ExtensionGoneError: the extension was reloaded or removed under this page.
export class ExtensionGoneError extends Error {
  override name = 'ExtensionGoneError'
}

function gone(e: unknown): boolean {
  return !chrome.runtime?.id || /context invalidated/i.test(e instanceof Error ? e.message : String(e))
}

function unwrap(reply: Reply | undefined): unknown {
  if (!reply) throw new Error('no reply from the background')
  if (!reply.ok) throw new Error(reply.error)
  return reply.value
}

export async function sendToBackground<M extends BackgroundRequest>(msg: M): Promise<BackgroundReply<M['type']>> {
  if (!chrome.runtime?.id) throw new ExtensionGoneError('extension context invalidated')
  let reply: Reply | undefined
  try {
    reply = (await chrome.runtime.sendMessage(msg)) as Reply | undefined
  } catch (e) {
    if (gone(e)) throw new ExtensionGoneError('extension context invalidated')
    throw e
  }
  return unwrap(reply) as BackgroundReply<M['type']>
}

// serveBackground answers runtime messages in the service worker.
export function serveBackground(handler: (msg: unknown) => Promise<unknown>, log: Log): void {
  chrome.runtime.onMessage.addListener((msg, _sender, sendResponse) => {
    handler(msg).then(
      (value) => sendResponse({ ok: true, value } satisfies Reply),
      (e: unknown) => {
        const error = e instanceof Error ? e.message : String(e)
        log.warn(`background: message failed: ${error}`)
        sendResponse({ ok: false, error } satisfies Reply)
      },
    )
    return true // keep the channel open for the async reply
  })
}

// sendToTab asks the content script in a tab; null when the tab has none.
export async function sendToTab(tabId: number, msg: TabRequest): Promise<CurrentPage | null> {
  try {
    return ((await chrome.tabs.sendMessage(tabId, msg)) as CurrentPage | undefined) ?? null
  } catch {
    return null
  }
}

// serveTabRequests answers the popup's questions in the content script.
export function serveTabRequests(handler: (msg: TabRequest) => CurrentPage): void {
  chrome.runtime.onMessage.addListener((msg, _sender, sendResponse) => {
    if ((msg as { type?: unknown })?.type === 'current-page') sendResponse(handler(msg as TabRequest))
    return false
  })
}
