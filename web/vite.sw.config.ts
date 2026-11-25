import { defineConfig } from 'vite'

// The service worker (src/sw.ts), a second build after the app's: library
// mode as an iife, so /sw.js is one file without imports, next to the app in
// dist/app (docs/contributing/web-ui.md, "Service worker"). It runs after
// `vite build`, so it must not empty the folder or copy public/ again.
export default defineConfig({
  build: {
    outDir: 'dist/app',
    emptyOutDir: false,
    copyPublicDir: false,
    lib: {
      entry: 'src/sw.ts',
      formats: ['iife'],
      // Required for an iife; sw.ts exports nothing, so it stays unused.
      name: 'deskServiceWorker',
      fileName: () => 'sw.js',
    },
  },
})
