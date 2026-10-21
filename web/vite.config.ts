import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// The Go binary embeds dist/app (see embed.go). During development, `npm run
// dev` proxies the API and the login to a UI started with `make run-ui`.
const backend = process.env.UI_BACKEND ?? 'http://127.0.0.1:9080'

export default defineConfig({
  plugins: [vue()],
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
