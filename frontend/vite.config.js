import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { fileURLToPath } from 'node:url'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url))
    }
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    rollupOptions: {
      external: ['/wails/runtime.js']
    }
  },
  server: {
    port: 5173,
    strictPort: false,
    watch: {
      usePolling: true,
      interval: 100
    },
    proxy: {
      '/wails/ws': {
        target: 'ws://127.0.0.1:4511',
        ws: true
      }
    }
  }
})
