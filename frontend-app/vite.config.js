import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import process from 'node:process';

function parseFrontendWatchBool(name, value) {
  if (value === undefined) {
    return undefined;
  }
  switch (value) {
    case '1':
    case 'true':
    case 'TRUE':
    case 'yes':
    case 'YES':
    case 'on':
    case 'ON':
      return true;
    case '0':
    case 'false':
    case 'FALSE':
    case 'no':
    case 'NO':
    case 'off':
    case 'OFF':
      return false;
    case '':
      throw new Error(`${name} must be a boolean (1/0, true/false, yes/no, on/off); got empty value`);
    default:
      throw new Error(`${name} must be a boolean (1/0, true/false, yes/no, on/off); got: ${value}`);
  }
}

function boolLabel(value) {
  return value ? '1' : '0';
}

function resolveFrontendWatchUsePolling(env) {
  const superDolphinPolling = parseFrontendWatchBool(
    'SUPER_DOLPHIN_VITE_USE_POLLING',
    env.SUPER_DOLPHIN_VITE_USE_POLLING,
  );
  const chokidarPolling = parseFrontendWatchBool('CHOKIDAR_USEPOLLING', env.CHOKIDAR_USEPOLLING);
  if (
    superDolphinPolling !== undefined &&
    chokidarPolling !== undefined &&
    superDolphinPolling !== chokidarPolling
  ) {
    throw new Error(
      `conflicting frontend watch config: SUPER_DOLPHIN_VITE_USE_POLLING resolves to ${boolLabel(superDolphinPolling)} but CHOKIDAR_USEPOLLING resolves to ${boolLabel(chokidarPolling)}`,
    );
  }
  if (superDolphinPolling !== undefined) {
    return superDolphinPolling;
  }
  if (chokidarPolling !== undefined) {
    return chokidarPolling;
  }
  return true;
}

export function createFrontendViteConfig(env = process.env) {
  const backendAddr = env.SUPER_DOLPHIN_HTTP_ADDR || '127.0.0.1:4512';
  const usePolling = resolveFrontendWatchUsePolling(env);

  return defineConfig({
    plugins: [react()],
    build: {
      outDir: 'dist',
      emptyOutDir: true,
      chunkSizeWarningLimit: 650,
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
      watch: {
        usePolling,
      },
      proxy: {
        '/wails/ws': {
          target: `ws://${backendAddr}`,
          ws: true,
        },
        '/generated-image': {
          target: `http://${backendAddr}`,
        },
        '/local-image': {
          target: `http://${backendAddr}`,
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
      exclude: ['**/node_modules/**', '**/dist/**', '**/.agents/**', '**/tests/e2e/**'],
      globals: true,
      setupFiles: './src/test-setup.js',
    },
  });
}

export default createFrontendViteConfig();
