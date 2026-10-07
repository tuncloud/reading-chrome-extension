import { expect, test } from 'vitest'
import { apiStub, INSTALLATION, testDb } from '../../test/helpers'
import { NotConnectedError } from '../shared/errors'
import { SetupService } from './setup'

test('searchPages needs a token and passes the query', async () => {
  const store = testDb()
  const api = apiStub({ setupPages: async () => [{ id: 'p1', title: 'Reading', url: 'https://www.notion.so/p1' }] })
  const svc = new SetupService({ db: store, api })
  await expect(svc.searchPages('Read')).rejects.toBeInstanceOf(NotConnectedError)
  await store.setKv('device_token', 'rt_x')
  expect(await svc.searchPages('Read')).toEqual([{ id: 'p1', title: 'Reading', url: 'https://www.notion.so/p1' }])
  expect(api.calls.at(-1)).toEqual({ method: 'setupPages', args: ['rt_x', 'Read'] })
})

test('createDatabases stores the now-active installation (§2.7)', async () => {
  const store = testDb()
  await store.setKv('device_token', 'rt_x')
  await store.setKv('installation', { ...INSTALLATION, status: 'needs_setup', setupError: 'template_not_found', libraryUrl: null })
  const api = apiStub()
  expect(await new SetupService({ db: store, api }).createDatabases('p1')).toEqual(INSTALLATION)
  expect(api.calls.at(-1)).toEqual({ method: 'setup', args: ['rt_x', 'p1'] })
  expect(await store.getKv('installation')).toEqual(INSTALLATION)
})
