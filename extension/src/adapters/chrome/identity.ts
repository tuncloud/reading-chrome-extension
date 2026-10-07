import type { AuthFlow } from '../../application/ports'

// ChromeAuthFlow runs OAuth in a Chrome-owned window (spec T29). The redirect is
// https://<extension-id>.chromiumapp.org/, which the backend allow-lists (§2.1).
export class ChromeAuthFlow implements AuthFlow {
  redirectUrl(): string {
    return chrome.identity.getRedirectURL()
  }

  async launch(url: string): Promise<string> {
    const redirect = await chrome.identity.launchWebAuthFlow({ url, interactive: true })
    if (!redirect) throw new Error('auth flow ended without a redirect')
    return redirect
  }
}
