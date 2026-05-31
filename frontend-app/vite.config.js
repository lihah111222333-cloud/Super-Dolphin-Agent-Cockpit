import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import process from 'node:process';

const backendAddr = process.env.SUPER_DOLPHIN_HTTP_ADDR || '127.0.0.1:4512';

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    rollupOptions: {
      external: ['/wails/runtime.js'],
    },
  },
  server: {
    port: 5175,
    strictPort: true,
    proxy: {
      '/wails/ws': {
        target: `ws://${backendAddr}`,
        ws: true,
      },
    },
  },
  test: {
    environment: 'jsdom',
    environmentOptions: {
      jsdom: {
        url: 'http://127.0.0.1:5175/',
      },
    },
    exclude: ['**/node_modules/**', '**/dist/**', '**/.agents/**'],
    globals: true,
    setupFiles: './src/test-setup.js',
  },
});
