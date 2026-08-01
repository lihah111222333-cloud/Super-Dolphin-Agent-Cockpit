import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import process from 'node:process';
import vitestSuitePolicyDefinition from './config/vitest-suite-policy.json';

const WAILS_RUNTIME_PATHNAME = '/wails/runtime.js';
const DEV_WAILS_RUNTIME_SHIM_PATH = join(process.cwd(), 'public', 'wails', 'runtime.js');
export const ATH_HEALTH_PATH = '/__ath_health';
export const ATH_NONCE_HEADER = 'x-agentic-testing-harness-nonce';
export const ATH_SOURCE_ROOT_HEADER = 'x-super-dolphin-source-root';
export const ATH_BUILD_IDENTITY_HEADER = 'x-super-dolphin-build-identity';

export function validateVitestSuitePolicy(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)
    || Object.keys(value).sort().join(',') !== 'defaultExcludes,schemaVersion'
    || value.schemaVersion !== 1
    || !Array.isArray(value.defaultExcludes)
    || value.defaultExcludes.length === 0
    || value.defaultExcludes.some((pattern) => typeof pattern !== 'string' || pattern.trim() !== pattern || pattern === '')
    || new Set(value.defaultExcludes).size !== value.defaultExcludes.length) {
    throw new Error('Vitest suite policy is invalid');
  }
  return Object.freeze({
    schemaVersion: value.schemaVersion,
    defaultExcludes: Object.freeze([...value.defaultExcludes]),
  });
}

export const VITEST_SUITE_POLICY = validateVitestSuitePolicy(
  vitestSuitePolicyDefinition,
);

function requiredAthValue(env, name) {
  const value = env[name];
  if (typeof value !== 'string' || value.trim() === '' || /[\r\n]/u.test(value)) {
    throw new Error(`${name} must be a non-empty string without CR or LF`);
  }
  return value;
}

export function agenticHarnessIdentityPlugin(env = process.env, viteEnv = {}) {
  const identityConfigured = [
    env.ATH_TARGET_NONCE,
    env.SUPER_DOLPHIN_ATH_SOURCE_ROOT,
    env.SUPER_DOLPHIN_ATH_BUILD_IDENTITY,
  ].some((value) => value !== undefined);
  if (!identityConfigured) {
    return undefined;
  }
  if (isProductionViteInvocation(env, viteEnv)) {
    throw new Error('agentic harness identity is dev/test-only and must not be set for production builds');
  }
  const nonce = requiredAthValue(env, 'ATH_TARGET_NONCE');
  if (!/^[A-Za-z0-9_-]{43}$/u.test(nonce)) {
    throw new Error('ATH_TARGET_NONCE must be a 256-bit base64url value');
  }
  const sourceRoot = requiredAthValue(env, 'SUPER_DOLPHIN_ATH_SOURCE_ROOT');
  const buildIdentity = requiredAthValue(env, 'SUPER_DOLPHIN_ATH_BUILD_IDENTITY');
  return {
    name: 'super-dolphin-agentic-harness-identity',
    apply: 'serve',
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        const pathname = new URL(req.url || '/', 'http://127.0.0.1').pathname;
        if (pathname !== ATH_HEALTH_PATH) {
          next();
          return;
        }
        res.statusCode = 200;
        res.setHeader('Content-Type', 'application/json; charset=utf-8');
        res.setHeader('Cache-Control', 'no-store');
        res.setHeader(ATH_NONCE_HEADER, nonce);
        res.setHeader(ATH_SOURCE_ROOT_HEADER, sourceRoot);
        res.setHeader(ATH_BUILD_IDENTITY_HEADER, buildIdentity);
        res.end(JSON.stringify({ ok: true, sourceRoot, buildIdentity }));
      });
    },
  };
}

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
  const harnessIdentity = agenticHarnessIdentityPlugin(env, viteEnv);

  return defineConfig({
    plugins: [
      serveDevelopmentWailsRuntimePlugin(),
      ...(harnessIdentity === undefined ? [] : [harnessIdentity]),
      react(),
    ],
    build: {
      outDir: 'dist',
      emptyOutDir: true,
      minify: 'terser',
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
            // Ant Design 体系独立成块，避免主入口 chunk 超出冻结预算。
            if (
              id.includes('/node_modules/antd/')
              || id.includes('/node_modules/@ant-design/')
              || id.includes('/node_modules/@rc-component/')
              || id.includes('/node_modules/rc-')
            ) {
              return 'antd';
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
      exclude: [...VITEST_SUITE_POLICY.defaultExcludes],
      globals: true,
      setupFiles: './src/test-setup.js',
    },
  });
}

export default defineConfig((viteEnv) => createFrontendViteConfig(process.env, viteEnv));
