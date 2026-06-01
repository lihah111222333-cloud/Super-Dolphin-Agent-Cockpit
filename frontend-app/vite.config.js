import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import process from 'node:process';

const backendAddr = process.env.SUPER_DOLPHIN_HTTP_ADDR || '127.0.0.1:4512';

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    rolldownOptions: {
      external: ['/wails/runtime.js'],
      output: {
        manualChunks(id) {
          if (id.includes('/node_modules/react/') || id.includes('/node_modules/react-dom/') || id.includes('/node_modules/scheduler/')) {
            return 'react-core';
          }
          if (id.includes('/node_modules/@tanstack/') || id.includes('/node_modules/zustand/')) {
            return 'query-state';
          }
          if (id.includes('/node_modules/lucide-react/')) {
            return 'icons';
          }
          return undefined;
        },
      },
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
