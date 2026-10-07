// SetupService finishes a needs_setup installation (spec §2.6, §2.7, T31).
import { NotConnectedError } from '../shared/errors'
import type { ApiClient } from './api'
import type { Db } from './db'
import type { Installation, NotionPage } from '../shared/types'

export interface SetupDeps {
  db: Db
  api: Pick<ApiClient, 'setupPages' | 'setup'>
}

export class SetupService {
  constructor(private readonly deps: SetupDeps) {}

  async searchPages(query: string): Promise<NotionPage[]> {
    return this.deps.api.setupPages(await this.token(), query)
  }

  async createDatabases(parentPageId: string): Promise<Installation> {
    const inst = await this.deps.api.setup(await this.token(), parentPageId)
    await this.deps.db.setKv('installation', inst)
    return inst
  }

  private async token(): Promise<string> {
    const token = await this.deps.db.getKv('device_token')
    if (!token) throw new NotConnectedError()
    return token
  }
}
