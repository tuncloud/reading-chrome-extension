// Dependency rules for the feature-folder layout (plan Global Constraints).
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, posix, resolve } from 'node:path'
import { describe, expect, test } from 'vitest'

const SRC = resolve(import.meta.dirname, '../src')

function walk(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const full = join(dir, name)
    return statSync(full).isDirectory() ? walk(full) : [full]
  })
}

export function sourceFiles(): string[] {
  return walk(SRC)
    .map((f) => f.slice(SRC.length + 1).split('\\').join('/'))
    .filter((f) => /\.tsx?$/.test(f) && !/\.test\.tsx?$/.test(f))
}

// stripComments drops // and /* */ comments but keeps string and template literals intact.
export function stripComments(code: string): string {
  let out = ''
  let i = 0
  while (i < code.length) {
    const c = code[i]!
    const next = code[i + 1]
    if (c === '"' || c === "'" || c === '`') {
      let j = i + 1
      while (j < code.length && code[j] !== c) j += code[j] === '\\' ? 2 : 1
      out += code.slice(i, j + 1)
      i = j + 1
    } else if (c === '/' && next === '/') {
      while (i < code.length && code[i] !== '\n') i++
    } else if (c === '/' && next === '*') {
      const end = code.indexOf('*/', i + 2)
      i = end === -1 ? code.length : end + 2
    } else {
      out += c
      i++
    }
  }
  return out
}

export function importsOf(code: string): string[] {
  const re = /(?:import|export)\s+(?:type\s+)?(?:[^'"]*?\s+from\s+)?['"]([^'"]+)['"]|import\(\s*['"]([^'"]+)['"]\s*\)/g
  return [...stripComments(code).matchAll(re)].map((m) => (m[1] ?? m[2]) as string)
}

export type Target = { rel: string } | { pkg: string }

function resolveImport(fromRel: string, spec: string): Target {
  if (!spec.startsWith('.')) return { pkg: spec.startsWith('@') ? spec.split('/').slice(0, 2).join('/') : spec.split('/')[0]! }
  return { rel: posix.normalize(posix.join(posix.dirname(fromRel), spec)) }
}

// LEGACY folders are still being emptied (Tasks 2-6); they carry no restrictions yet. Remove with Task 6.
const LEGACY = new Set(['application', 'adapters', 'ui', 'entry'])

const top = (rel: string) => rel.split('/')[0] ?? ''

// violation explains why fromRel may not import target, or returns null.
export function violation(fromRel: string, target: Target): string | null {
  const from = top(fromRel)
  if ('pkg' in target) {
    if ((target.pkg === 'react' || target.pkg === 'react-dom') && from !== 'popup' && from !== 'options' && !LEGACY.has(from)) {
      return `${fromRel}: react is only for popup/ and options/`
    }
    return null
  }
  const to = top(target.rel)
  if (from === 'content' && to !== 'content' && to !== 'shared') return `${fromRel}: content may import only content/ and shared/ (${target.rel})`
  if (from === 'shared' && to !== 'shared') return `${fromRel}: shared may import only shared/ (${target.rel})`
  if (from === 'background' && (to === 'popup' || to === 'options' || to === 'content')) return `${fromRel}: background may not import ${to}/`
  return null
}

function candidates(rel: string): string[] {
  return /\.tsx?$/.test(rel) ? [rel] : [`${rel}.ts`, `${rel}.tsx`, `${rel}/index.ts`, `${rel}/index.tsx`]
}

// reactImporters walks the relative-import closure of entry over files and returns the files that import react.
export function reactImporters(entry: string, files: Map<string, string>): string[] {
  const seen = new Set<string>()
  const hits: string[] = []
  const queue = [entry]
  while (queue.length > 0) {
    const f = queue.pop()!
    if (seen.has(f) || !files.has(f)) continue
    seen.add(f)
    for (const spec of importsOf(files.get(f)!)) {
      const t = resolveImport(f, spec)
      if ('pkg' in t) {
        if (t.pkg === 'react' || t.pkg === 'react-dom') hits.push(`${f} imports ${t.pkg}`)
      } else {
        const found = candidates(t.rel).find((c) => files.has(c))
        if (found) queue.push(found)
      }
    }
  }
  return hits
}

describe('dependency rules', () => {
  const files = sourceFiles()
  const sources = new Map(files.map((f) => [f, readFileSync(join(SRC, f), 'utf8')]))

  test('the walk finds the source tree (not vacuous)', () => {
    expect(files).toContain('content/index.ts')
    expect(files).toContain('shared/messages.ts')
  })

  test('every import respects the folder rules', () => {
    const problems = files.flatMap((f) => importsOf(sources.get(f)!).map((s) => violation(f, resolveImport(f, s))).filter(Boolean))
    expect(problems).toEqual([])
  })

  test.each(['content/index.ts', 'background/index.ts'])('%s import closure never reaches react', (entry) => {
    expect(sources.has(entry)).toBe(true)
    expect(reactImporters(entry, sources)).toEqual([])
  })
})

describe('rule table', () => {
  test.each([
    ['content/driver.ts', { rel: 'content/tracker' }, false],
    ['content/driver.ts', { rel: 'shared/reading' }, false],
    ['content/index.ts', { rel: 'background/db' }, true],
    ['content/index.ts', { rel: 'popup/model' }, true],
    ['shared/url.ts', { rel: 'background/api' }, true],
    ['shared/url.ts', { rel: 'shared/medium' }, false],
    ['background/sync.ts', { rel: 'shared/types' }, false],
    ['background/sync.ts', { rel: 'popup/model' }, true],
    ['popup/model.ts', { rel: 'background/db' }, false],
    ['options/controller.ts', { rel: 'popup/messages' }, false],
    ['popup/App.tsx', { pkg: 'react' }, false],
    ['background/index.ts', { pkg: 'react' }, true],
    ['content/index.ts', { pkg: 'react-dom' }, true],
    ['background/db.ts', { pkg: 'dexie' }, false],
  ] as const)('%s → %j forbidden=%s', (from, target, forbidden) => {
    expect(violation(from, target) !== null).toBe(forbidden)
  })

  test('the closure walker follows .ts/.tsx/index and reports react', () => {
    const files = new Map([
      ['a/entry.ts', "import { b } from './b'\nimport x from './dir'"],
      ['a/b.tsx', "import React from 'react'"],
      ['a/dir/index.ts', "export const y = 1"],
      ['a/view/index.tsx', "import { createRoot } from 'react-dom'"],
    ])
    files.set('a/entry.ts', "import { b } from './b'\nimport x from './dir'\nimport v from './view'")
    expect(reactImporters('a/entry.ts', files)).toEqual(['a/view/index.tsx imports react-dom', 'a/b.tsx imports react'])
  })

  test('comment markers inside strings do not hide imports', () => {
    expect(importsOf("const p = ['https://*/*']\nimport x from '../y'")).toEqual(['../y'])
  })
})
