import '@testing-library/jest-dom/vitest';

if (typeof window !== 'undefined' && !window.localStorage) {
  const storage = new Map();

  Object.defineProperty(window, 'localStorage', {
    configurable: true,
    value: {
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
    },
  });
}
