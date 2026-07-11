import '@testing-library/jest-dom/vitest';
import { configure } from '@testing-library/dom';
import { beforeEach } from 'vitest';

configure({ asyncUtilTimeout: 5000 });

const OVERLAY_ROOT_FIXTURE_ERROR = 'Test overlay-root fixture requires at most one host.';

beforeEach(() => {
  const hosts = document.querySelectorAll('#overlay-root');
  if (hosts.length > 1) throw new Error(OVERLAY_ROOT_FIXTURE_ERROR);
  if (hosts.length === 1) return;
  const overlayRoot = document.createElement('div');
  overlayRoot.id = 'overlay-root';
  document.body.append(overlayRoot);
});

function createMemoryStorage() {
  const storage = new Map();

  return {
    clear() {
      storage.clear();
    },
    getItem(key) {
      const normalizedKey = String(key);
      return storage.has(normalizedKey) ? storage.get(normalizedKey) : null;
    },
    key(index) {
      return Array.from(storage.keys())[index] ?? null;
    },
    removeItem(key) {
      storage.delete(String(key));
    },
    setItem(key, value) {
      storage.set(String(key), String(value));
    },
    get length() {
      return storage.size;
    },
  };
}

if (typeof window !== 'undefined') {
  const localStorage = createMemoryStorage();
  Object.defineProperty(window, 'localStorage', {
    configurable: true,
    value: localStorage,
  });

  if (globalThis !== window) {
    Object.defineProperty(globalThis, 'localStorage', {
      configurable: true,
      value: localStorage,
    });
  }
}
