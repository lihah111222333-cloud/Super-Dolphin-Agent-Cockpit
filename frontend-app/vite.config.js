import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import process from 'node:process';

const WAILS_RUNTIME_PATHNAME = '/wails/runtime.js';
const DEV_WAILS_RUNTIME_SHIM_PATH = join(process.cwd(), 'public', 'wails', 'runtime.js');

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

function resolveWailsWebSocketProxyHeaders(env) {
  const token = (
    env.SUPER_DOLPHIN_WAILS_WS_TOKEN ||
    env.GO_AGENT_CTL_SESSION_TOKEN ||
    env.GO_AGENT_MCP_SESSION_TOKEN ||
    ''
  ).trim();
  if (!token) {
    return undefined;
  }
  if (/[\r\n;]/.test(token)) {
    throw new Error('wails websocket proxy token must not contain CR, LF, or semicolon');
  }
  return { Cookie: `super_dolphin_wails_ws=${token}` };
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

function isProductionViteInvocation(env, viteEnv = {}) {
  return viteEnv.command === 'build' || viteEnv.mode === 'production' || env.NODE_ENV === 'production';
}

function serveDevelopmentWailsRuntimePlugin() {
  return {
    name: 'super-dolphin-dev-wails-runtime',
    apply: 'serve',
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        const pathname = new URL(req.url || '/', 'http://127.0.0.1').pathname;
        if (pathname !== WAILS_RUNTIME_PATHNAME) {
          next();
          return;
        }
        const source = readFileSync(DEV_WAILS_RUNTIME_SHIM_PATH, 'utf8');
        res.statusCode = 200;
        res.setHeader('Content-Type', 'text/javascript; charset=utf-8');
        res.setHeader('Cache-Control', 'no-store');
        res.end(source);
      });
    },
  };
}

function assertUITestMCPFlagAllowed(env, viteEnv = {}) {
  if (env.VITE_SUPER_DOLPHIN_UI_TEST_MCP === undefined) {
    return;
  }
  if (isProductionViteInvocation(env, viteEnv)) {
    throw new Error('VITE_SUPER_DOLPHIN_UI_TEST_MCP is dev/test-only and must not be set for production builds');
  }
}

export function createFrontendViteConfig(env = process.env, viteEnv = {}) {
  assertUITestMCPFlagAllowed(env, viteEnv);
  const backendAddr = env.SUPER_DOLPHIN_HTTP_ADDR || '127.0.0.1:4512';
  const usePolling = resolveFrontendWatchUsePolling(env);
  const wailsWebSocketProxyHeaders = resolveWailsWebSocketProxyHeaders(env);

  return defineConfig({
    plugins: [serveDevelopmentWailsRuntimePlugin(), react()],
    build: {
      outDir: 'dist',
      emptyOutDir: true,
      chunkSizeWarningLimit: 650,
      rolldownOptions: {
        input: {
          main: join(process.cwd(), 'index.html'),
          recovery: join(process.cwd(), 'recovery.html'),
        },
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
          ...(wailsWebSocketProxyHeaders ? { headers: wailsWebSocketProxyHeaders } : {}),
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

export default defineConfig((viteEnv) => createFrontendViteConfig(process.env, viteEnv));
