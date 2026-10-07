import type { ContentScripts } from '../../application/ports'

// ChromeContentScripts registers the content script on user-enabled domains (spec T31).
export class ChromeContentScripts implements ContentScripts {
  constructor(private readonly file = 'content.js') {}

  async register(id: string, matches: string[]): Promise<void> {
    const script = { id, matches, js: [this.file], runAt: 'document_idle' as const, persistAcrossSessions: true }
    if ((await this.registeredIds()).includes(id)) await chrome.scripting.updateContentScripts([script])
    else await chrome.scripting.registerContentScripts([script])
  }

  async unregister(ids: string[]): Promise<void> {
    const known = new Set(await this.registeredIds())
    const present = ids.filter((id) => known.has(id))
    if (present.length > 0) await chrome.scripting.unregisterContentScripts({ ids: present })
  }

  async registeredIds(): Promise<string[]> {
    return (await chrome.scripting.getRegisteredContentScripts()).map((s) => s.id)
  }
}
