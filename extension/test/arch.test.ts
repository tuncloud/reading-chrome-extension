// The dependency rule (plan Global Constraints): dependencies point inward.
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, posix, resolve } from 'node:path'
import { describe, expect, test } from 'vitest'

const SRC = resolve(import.meta.dirname, '../src')
const LAYERS = ['domain', 'application', 'adapters', 'ui', 'entry'] as const
type Layer = (typeof LAYERS)[number]

export type Target = { rel: string } | { pkg: string }

function walk(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const full = join(dir, name)
    return statSync(full).isDirectory() ? walk(full) : [full]
  })
}

// sourceFiles lists production .ts/.tsx files under src/ as posix paths relative to src/.
export function sourceFiles(): string[] {
  return walk(SRC)
    .map((f) => f.slice(SRC.length + 1).split('\\').join('/'))
    .filter((f) => /\.tsx?$/.test(f) && !/\.test\.tsx?$/.test(f))
}

function layerOf(rel: string): { layer: Layer | null; sub: string } {
  const [first, second = ''] = rel.split('/')
  const layer = (LAYERS as readonly string[]).includes(first ?? '') ? (first as Layer) : null
  return { layer, sub: layer === 'adapters' ? second : '' }
}

// stripComments drops // and /* */ comments but keeps string and template literals intact,
// so "https://*/*" does not start a comment.
function stripComments(code: string): string {
  let out = ''
  let i = 0
  while (i < code.length) {
    const c = code[i]!
    const next = code[i + 1]
    if (c === "'" || c === '"' || c === '`') {
      let j = i + 1
      while (j < code.length && code[j] !== c) j += code[j] === '\\' ? 2 : 1
      out += code.slice(i, j + 1)
      i = j + 1
    } else if (c === '/' && next === '/') {
      while (i < code.length && code[i] !== '\n') i++
    } else if (c === '/' && next === '*') {
      const end = code.indexOf('*/', i + 2)
      i = end === -1 ? code.length : end + 2
      out += ' '
    } else {
      out += c
      i++
    }
  }
  return out
}

export function importsOf(code: string): string[] {
  const out: string[] = []
  const re = /(?:import|export)\s+(?:type\s+)?(?:[^'"]*?\s+from\s+)?['"]([^'"]+)['"]|import\(\s*['"]([^'"]+)['"]\s*\)/g
  for (const m of stripComments(code).matchAll(re)) out.push((m[1] ?? m[2]) as string)
  return out
}

function resolveImport(fromRel: string, spec: string): Target {
  if (!spec.startsWith('.')) return { pkg: spec.startsWith('@') ? spec.split('/').slice(0, 2).join('/') : spec.split('/')[0]! }
  const joined = posix.normalize(posix.join(posix.dirname(fromRel), spec))
  return { rel: /\.tsx?$/.test(joined) ? joined : `${joined}.ts` }
}

// violation explains why fromRel may not import target, or returns null.
export function violation(fromRel: string, target: Target): string | null {
  const from = layerOf(fromRel)
  if (from.layer === null) return `${fromRel} is outside the layer directories ${LAYERS.join(', ')}`
  if (from.layer === 'entry') return null
  if ('pkg' in target) {
    if (target.pkg === 'dexie' && from.layer === 'adapters' && from.sub === 'storage') return null
    if ((target.pkg === 'react' || target.pkg === 'react-dom') && from.layer === 'ui') return null
    return `${from.layer} may not import the package ${target.pkg}`
  }
  const to = layerOf(target.rel)
  if (to.layer === null) return `${target.rel} is outside src/ layers`
  const allowed: Record<Layer, Layer[]> = {
    domain: ['domain'],
    application: ['domain', 'application'],
    adapters: ['domain', 'application', 'adapters'],
    ui: ['domain', 'application', 'ui'],
    entry: [...LAYERS],
  }
  if (!allowed[from.layer].includes(to.layer)) return `${from.layer} may not import ${to.layer} (${target.rel})`
  if (from.layer === 'adapters' && to.layer === 'adapters' && from.sub !== to.sub) {
    return `adapters/${from.sub} may not import adapters/${to.sub}`
  }
  return null
}

// resolveFile finds the source file a relative import points at, or null when it is not in files.
export function resolveFile(fromRel: string, spec: string, files: ReadonlySet<string>): string | null {
  const joined = posix.normalize(posix.join(posix.dirname(fromRel), spec))
  const candidates = /\.tsx?$/.test(joined)
    ? [joined]
    : [`${joined}.ts`, `${joined}.tsx`, `${joined}/index.ts`, `${joined}/index.tsx`]
  return candidates.find((c) => files.has(c)) ?? null
}

// reactImporters walks the relative-import closure of entry and returns the files in it that
// import react or react-dom, as "file imports pkg".
export function reactImporters(entry: string, sources: ReadonlyMap<string, string>): string[] {
  const files = new Set(sources.keys())
  const seen = new Set<string>()
  const found: string[] = []
  const todo = [entry]
  while (todo.length > 0) {
    const f = todo.pop()!
    if (seen.has(f)) continue
    seen.add(f)
    for (const spec of importsOf(sources.get(f) ?? '')) {
      if (spec.startsWith('.')) {
        const next = resolveFile(f, spec, files)
        if (next) todo.push(next)
      } else {
        const pkg = resolveImport(f, spec)
        if ('pkg' in pkg && (pkg.pkg === 'react' || pkg.pkg === 'react-dom')) found.push(`${f} imports ${pkg.pkg}`)
      }
    }
  }
  return found.sort()
}

// globalViolation flags browser globals used outside the layers allowed to touch them.
export function globalViolation(fromRel: string, code: string): string | null {
  const { layer, sub } = layerOf(fromRel)
  const body = stripComments(code)
  if (/\bchrome\s*(\?\.|\.|\[)/.test(body) && layer !== 'entry' && !(layer === 'adapters' && sub === 'chrome')) {
    return `${fromRel} uses chrome.* outside adapters/chrome and entry`
  }
  if ((layer === 'domain' || layer === 'application') && /\b(window|document)\s*(\?\.|\.|\[)/.test(body)) {
    return `${fromRel} touches window/document in ${layer}`
  }
  return null
}

describe('layer rule', () => {
  const files = sourceFiles()

  test('the walk finds every layer (not vacuous)', () => {
    for (const f of [
      'domain/tracker.ts', 'application/sync.ts', 'adapters/storage/dexie-store.ts',
      'adapters/chrome/messaging.ts', 'ui/popup/Popup.tsx', 'ui/options/OptionsView.tsx', 'entry/background.ts', 'entry/content.ts',
    ]) {
      expect(files).toContain(f)
    }
  })

  test('every import points inward', () => {
    const problems: string[] = []
    for (const f of files) {
      const code = readFileSync(join(SRC, f), 'utf8')
      for (const spec of importsOf(code)) {
        const v = violation(f, resolveImport(f, spec))
        if (v) problems.push(v)
      }
      const g = globalViolation(f, code)
      if (g) problems.push(g)
    }
    expect(problems).toEqual([])
  })
})

describe('content and background bundles stay React-free', () => {
  const sources = new Map(sourceFiles().map((f) => [f, readFileSync(join(SRC, f), 'utf8')]))

  test.each(['entry/content.ts', 'entry/background.ts'])('%s import closure never reaches react', (entry) => {
    expect(sources.has(entry)).toBe(true)
    expect(reactImporters(entry, sources)).toEqual([])
  })
})

describe('reactImporters', () => {
  const sources = new Map([
    ['entry/a.ts', "import { b } from '../adapters/x/b'\nimport './side'"],
    ['adapters/x/b.ts', "import { c } from '../../ui/c'"],
    ['ui/c/index.tsx', "import { useState } from 'react'"],
    ['entry/side.ts', "export const s = 1"],
    ['entry/clean.ts', "import { s } from './side'"],
    ['entry/dyn.ts', "const m = import('react-dom/client')"],
    ['entry/cycle.ts', "import './cycle'\nimport '@scope/pkg/x'"],
    ['entry/comment.ts', "// import 'react'\nexport {}"],
  ])

  test.each([
    ['entry/a.ts', ['ui/c/index.tsx imports react']],
    ['entry/clean.ts', []],
    ['entry/dyn.ts', ['entry/dyn.ts imports react-dom']],
    ['entry/cycle.ts', []],
    ['entry/comment.ts', []],
  ] as const)('closure of %s', (entry, expected) => {
    expect(reactImporters(entry, sources)).toEqual(expected)
  })

  test('resolveFile tries .ts, .tsx and index files, and gives up on unknown paths', () => {
    const files = new Set(['a/b.ts', 'a/c.tsx', 'a/d/index.ts', 'a/e/index.tsx', 'a/f.tsx'])
    expect(resolveFile('a/x.ts', './b', files)).toBe('a/b.ts')
    expect(resolveFile('a/x.ts', './c', files)).toBe('a/c.tsx')
    expect(resolveFile('a/x.ts', './d', files)).toBe('a/d/index.ts')
    expect(resolveFile('a/x.ts', './e', files)).toBe('a/e/index.tsx')
    expect(resolveFile('a/x.ts', './f.tsx', files)).toBe('a/f.tsx')
    expect(resolveFile('a/x.ts', './missing', files)).toBeNull()
    expect(resolveFile('a/y/x.ts', '../b', files)).toBe('a/b.ts')
  })
})

describe('violation rules', () => {
  test.each([
    ['domain/url.ts', { rel: 'domain/medium.ts' }, false],
    ['domain/url.ts', { rel: 'application/ports.ts' }, true],
    ['domain/url.ts', { pkg: 'dexie' }, true],
    ['application/sync.ts', { rel: 'domain/reading.ts' }, false],
    ['application/sync.ts', { rel: 'adapters/http/api-client.ts' }, true],
    ['application/sync.ts', { rel: 'ui/popup/Popup.tsx' }, true],
    ['adapters/http/api-client.ts', { rel: 'application/ports.ts' }, false],
    ['adapters/http/api-client.ts', { rel: 'adapters/http/wire.ts' }, false],
    ['adapters/http/api-client.ts', { rel: 'adapters/storage/dexie-store.ts' }, true],
    ['adapters/storage/dexie-store.ts', { pkg: 'dexie' }, false],
    ['adapters/http/api-client.ts', { pkg: 'dexie' }, true],
    ['ui/popup/Popup.tsx', { rel: 'application/popup-model.ts' }, false],
    ['ui/popup/Popup.tsx', { rel: 'adapters/chrome/tabs.ts' }, true],
    ['ui/popup/Popup.tsx', { pkg: 'react' }, false],
    ['entry/popup.tsx', { pkg: 'react-dom' }, false],
    ['application/popup-model.ts', { pkg: 'react' }, true],
    ['adapters/page/tracker-driver.ts', { pkg: 'react' }, true],
    ['entry/background.ts', { rel: 'adapters/storage/dexie-store.ts' }, false],
    ['entry/content.ts', { pkg: 'react' }, false], // bundles are policed by the import-closure test
    ['lib/x.ts', { rel: 'domain/url.ts' }, true],
  ] as const)('%s → %j forbidden=%s', (from, target, forbidden) => {
    expect(violation(from, target) !== null).toBe(forbidden)
  })

  test.each([
    ['domain/tracker.ts', 'const x = chrome.runtime', true],
    ['application/sync.ts', 'document.title', true],
    ['application/sync.ts', '// a comment about document.title', false],
    ['adapters/chrome/tabs.ts', 'chrome.tabs.query({})', false],
    ['adapters/page/tracker-driver.ts', 'document.addEventListener', false],
    ['adapters/page/tracker-driver.ts', 'chrome.runtime.sendMessage', true],
    ['entry/content.ts', 'chrome.runtime.onMessage', false],
    ['domain/x.ts', 'chrome?.storage.get()', true],
    ['domain/x.ts', "chrome['storage']", true],
    ['application/x.ts', 'document?.title', true],
    ['domain/x.ts', 'const q = `https://*.${d}/*`\nchrome.storage.local.get()', true],
  ] as const)('%s uses %j forbidden=%s', (from, code, forbidden) => {
    expect(globalViolation(from, code) !== null).toBe(forbidden)
  })

  test('importsOf sees static, type-only, re-export and dynamic imports', () => {
    const code = `import a from './a'\nimport type { B } from '../b'\nexport { c } from "./c"\nconst d = import('./d')\nimport './side'`
    expect(importsOf(code)).toEqual(['./a', '../b', './c', './d', './side'])
  })

  test('importsOf is not fooled by comment markers inside string literals', () => {
    const code = "const p = ['https://*/*']\nimport x from '../adapters/y'"
    expect(importsOf(code)).toContain('../adapters/y')
    expect(importsOf("// import a from './a'\n/* import b from './b' */\nimport c from './c'")).toEqual(['./c'])
  })
})
