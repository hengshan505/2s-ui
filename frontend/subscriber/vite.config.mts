import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'node:url'

// The subscriber dashboard shares frontend/'s package.json and node_modules
// but is its own bundle: `npm run build:subscriber` (part of `npm run build`)
// runs vite with this config from frontend/. root pins everything below to
// this directory, so the cwd's index.html, public/ and dist/ -- the panel's --
// are never picked up, and the output lands in subscriber/dist.
//
// The dashboard is served from the subscription endpoint which is already at
// a deep path (/{subPath}/{clientName}), so all asset URLs must be relative to
// stay valid. base: '' keeps the emitted index.html referencing ./assets/...
// instead of an absolute /assets/... path.
export default defineConfig({
  root: fileURLToPath(new URL('.', import.meta.url)),
  base: '',
  plugins: [vue()],
  build: {
    outDir: 'dist',
    assetsDir: 'assets',
    chunkSizeWarningLimit: 1500,
    rollupOptions: {
      output: {
        entryFileNames: 'assets/[name]-[hash].js',
        chunkFileNames: 'assets/[name]-[hash].js',
        assetFileNames: 'assets/[name]-[hash][extname]',
      },
    },
  },
})
