import '@testing-library/jest-dom/vitest';
import { configure } from '@testing-library/dom';

configure({ asyncUtilTimeout: 5000 });

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
