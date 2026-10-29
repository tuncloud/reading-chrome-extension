import { describe, expect, test } from 'vitest'
import { apiHostPattern, buildManifest, STATIC_SITES } from './manifest.config'

describe('buildManifest', () => {
  const m = buildManifest({ apiBase: 'https://abc.lambda-url.ap-southeast-1.on.aws', version: '0.1.0' })

  test('requests only the T25 permissions and never <all_urls>', () => {
    expect(m.permissions).toEqual(['storage', 'alarms', 'identity', 'scripting'])
    expect(JSON.stringify(m)).not.toContain('<all_urls>')
    expect(m.optional_host_permissions).toEqual(['https://*/*'])
  })

  test('is not allowed in incognito (C24)', () => {
    expect(m.incognito).toBe('not_allowed')
  })

  test('host permissions are the static sites plus the API origin', () => {
    expect(m.host_permissions).toEqual([...STATIC_SITES, 'https://abc.lambda-url.ap-southeast-1.on.aws/*'])
  })

  test('content script runs on the static sites only', () => {
    expect(m.content_scripts).toEqual([{ matches: [...STATIC_SITES], js: ['content.js'], run_at: 'document_idle' }])
  })

  test('module service worker, popup and options page', () => {
    expect(m.background).toEqual({ service_worker: 'background.js', type: 'module' })
    expect(m.action).toEqual({
      default_popup: 'popup.html',
      default_icon: {
        16: 'icons/icon-16.png',
        32: 'icons/icon-32.png',
        48: 'icons/icon-48.png',
        128: 'icons/icon-128.png',
      },
    })
    expect(m.options_ui).toEqual({ page: 'options.html', open_in_tab: true })
  })

  test('uses the logo at every Chrome-supported size', () => {
    expect(m.icons).toEqual({
      16: 'icons/icon-16.png',
      32: 'icons/icon-32.png',
      48: 'icons/icon-48.png',
      128: 'icons/icon-128.png',
    })
  })

  test('key is included only when given', () => {
    expect(m.key).toBeUndefined()
    expect(buildManifest({ apiBase: 'http://localhost:8080', version: '1.0.0', key: 'MIIB' }).key).toBe('MIIB')
  })
})

describe('apiHostPattern', () => {
  test('drops the port and path: Chrome match patterns ignore ports', () => {
    expect(apiHostPattern('http://localhost:8080')).toBe('http://localhost/*')
    expect(apiHostPattern('https://api.example.com/base/')).toBe('https://api.example.com/*')
  })

  test('rejects non-http(s) and garbage', () => {
    expect(() => apiHostPattern('ftp://x.example')).toThrow(/VITE_API_BASE/)
    expect(() => apiHostPattern('not a url')).toThrow(/VITE_API_BASE/)
  })
})
