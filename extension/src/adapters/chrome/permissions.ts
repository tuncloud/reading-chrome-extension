import type { Permissions } from '../../application/ports'

// ChromePermissions requests optional host permissions; call request() from a user gesture.
export class ChromePermissions implements Permissions {
  request(origins: string[]): Promise<boolean> {
    return chrome.permissions.request({ origins })
  }

  remove(origins: string[]): Promise<boolean> {
    return chrome.permissions.remove({ origins })
  }
}
