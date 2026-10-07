// SetupService finishes a needs_setup installation (spec §2.6, §2.7, T31).
import { NotConnectedError } from './errors'
import type { Api, LocalStore } from './ports'
import type { Installation, NotionPage } from './types'

export interface SetupDeps {
  store: LocalStore
  api: Api
}

export class SetupService {
  constructor(private readonly deps: SetupDeps) {}

  async searchPages(query: string): Promise<NotionPage[]> {
    return this.deps.api.setupPages(await this.token(), query)
  }

  async createDatabases(parentPageId: string): Promise<Installation> {
    const inst = await this.deps.api.setup(await this.token(), parentPageId)
    await this.deps.store.setKv('installation', inst)
    return inst
  }

  private async token(): Promise<string> {
    const token = await this.deps.store.getKv('device_token')
    if (!token) throw new NotConnectedError()
    return token
  }
}
