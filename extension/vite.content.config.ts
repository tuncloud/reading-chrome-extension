import { resolve } from 'node:path'
import { defineConfig } from 'vite'

const root = import.meta.dirname

export default defineConfig({
  build: {
    outDir: 'dist',
    emptyOutDir: false,
    target: 'chrome120',
    lib: {
      entry: resolve(root, 'src/entry/content.ts'),
      formats: ['iife'],
      name: 'readingTrackerContent',
      fileName: () => 'content.js',
    },
  },
})
