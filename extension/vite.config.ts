import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import react from '@vitejs/plugin-react'
import { defineConfig, loadEnv, type Plugin } from 'vite'
import { buildManifest } from './manifest.config.ts'

const root = import.meta.dirname

function manifestPlugin(apiBase: string, key: string | undefined): Plugin {
  const pkg = JSON.parse(readFileSync(resolve(root, 'package.json'), 'utf8')) as { version: string }
  return {
    name: 'reading-tracker-manifest',
    generateBundle() {
      const manifest = buildManifest({ apiBase, version: pkg.version, key })
      this.emitFile({ type: 'asset', fileName: 'manifest.json', source: JSON.stringify(manifest, null, 2) })
    },
  }
}

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, root, '')
  const apiBase = env.VITE_API_BASE
  if (!apiBase) throw new Error('VITE_API_BASE is required: copy .env.example to .env.local')
  return {
    build: {
      outDir: 'dist',
      emptyOutDir: true,
      target: 'chrome120',
      // Chrome supports modulepreload natively; the polyfill would only add a chunk.
      modulePreload: { polyfill: false },
      rollupOptions: {
        input: {
          background: resolve(root, 'src/background/index.ts'),
          popup: resolve(root, 'popup.html'),
          options: resolve(root, 'options.html'),
        },
        output: {
          entryFileNames: '[name].js',
          chunkFileNames: 'chunks/[name]-[hash].js',
          assetFileNames: 'assets/[name]-[hash][extname]',
        },
      },
    },
    plugins: [react(), manifestPlugin(apiBase, env.EXTENSION_KEY || undefined)],
  }
})
