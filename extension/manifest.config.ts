// The MV3 manifest, built at bundle time so API_BASE and the optional key come from env.

// Sites the extension tracks without the user enabling them. New Freedium mirrors from
// /v1/config are not listed here: the user enables them as domains in Options (T31).
export const STATIC_SITES = [
  'https://freedium-mirror.cfd/*',
  'https://freedium.cfd/*',
  'https://medium.com/*',
  'https://*.medium.com/*',
] as const

export interface ManifestEnv {
  apiBase: string
  version: string
  key?: string
}

export interface Manifest {
  manifest_version: 3
  name: string
  description: string
  version: string
  icons: Record<'16' | '32' | '48' | '128', string>
  minimum_chrome_version: string
  permissions: string[]
  host_permissions: string[]
  optional_host_permissions: string[]
  background: { service_worker: string; type: 'module' }
  content_scripts: { matches: string[]; js: string[]; run_at: 'document_idle' }[]
  action: { default_popup: string; default_icon: Record<'16' | '32' | '48' | '128', string> }
  options_ui: { page: string; open_in_tab: boolean }
  incognito: 'not_allowed'
  key?: string
}

export const ICONS = {
  16: 'icons/icon-16.png',
  32: 'icons/icon-32.png',
  48: 'icons/icon-48.png',
  128: 'icons/icon-128.png',
} as const

// apiHostPattern turns VITE_API_BASE into a host permission. Chrome match patterns
// ignore ports, so http://localhost:8080 becomes http://localhost/*.
export function apiHostPattern(apiBase: string): string {
  let u: URL
  try {
    u = new URL(apiBase)
  } catch {
    throw new Error(`VITE_API_BASE is not a URL: ${apiBase}`)
  }
  if (u.protocol !== 'https:' && u.protocol !== 'http:') {
    throw new Error(`VITE_API_BASE must be http(s): ${apiBase}`)
  }
  return `${u.protocol}//${u.hostname}/*`
}

export function buildManifest(env: ManifestEnv): Manifest {
  const m: Manifest = {
    manifest_version: 3,
    name: 'Reading Tracker',
    description: 'Measures active reading time and logs it to your Notion.',
    version: env.version,
    icons: ICONS,
    minimum_chrome_version: '120',
    permissions: ['storage', 'alarms', 'identity', 'scripting'],
    host_permissions: [...STATIC_SITES, apiHostPattern(env.apiBase)],
    optional_host_permissions: ['https://*/*'],
    background: { service_worker: 'background.js', type: 'module' },
    content_scripts: [{ matches: [...STATIC_SITES], js: ['content.js'], run_at: 'document_idle' }],
    action: { default_popup: 'popup.html', default_icon: ICONS },
    options_ui: { page: 'options.html', open_in_tab: true },
    incognito: 'not_allowed',
  }
  if (env.key) m.key = env.key
  return m
}
