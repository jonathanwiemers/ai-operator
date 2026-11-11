import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import ui from '@nuxt/ui/vite'

// The Go binary embeds dist/app (see embed.go). During development, `npm run
// dev` proxies the API and the login to a UI started with `make run-ui`.
const backend = process.env.UI_BACKEND ?? 'http://127.0.0.1:9080'

export default defineConfig({
  plugins: [
    vue(),
    // Nuxt UI without the Nuxt framework (docs/ui.md, "UI kit").
    ui({
      // Explicit imports only: `npm run build` runs vue-tsc before Vite, so
      // the .d.ts files the auto-imports would generate do not exist yet.
      autoImport: false,
      components: false,
      // colorScheme.ts sets .dark; the theme's colours are static in
      // style.css. Fonts stay system fonts (the Vite plugin's fonts option is
      // off by default and not configurable).
      colorMode: false,
      // Theme classes only for the components the app uses.
      experimental: { componentDetection: true },
      // Icons are bundled from @iconify-json/lucide, so the Iconify API is
      // never asked (connect-src 'self'). `scan` finds literal names such as
      // icon="i-lucide-house" in src; names built at runtime go in `icons`.
      icon: {
        clientBundle: {
          scan: { globInclude: ['src/**/*.{vue,ts}'] },
          icons: [],
        },
      },
    }),
  ],
  build: {
    outDir: 'dist/app',
    emptyOutDir: true,
    assetsInlineLimit: 0,
  },
  server: {
    proxy: {
      '/api': { target: backend, changeOrigin: false },
      '/oauth2': { target: backend, changeOrigin: false },
    },
  },
})
