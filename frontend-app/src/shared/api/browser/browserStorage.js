export function requiredAppStoragePort(label = 'app storage') {
  if (typeof globalThis === 'undefined' || typeof window === 'undefined') {
    throw new Error(`${label} global object is unavailable`);
  }
  const storage = window.localStorage;
  if (
    !storage
    || typeof storage.getItem !== 'function'
    || typeof storage.setItem !== 'function'
    || typeof storage.removeItem !== 'function'
  ) {
    throw new Error(`${label} is unavailable`);
  }
  return {
    get(key) {
      return storage.getItem(key);
    },
    set(key, value) {
      storage.setItem(key, value);
    },
    remove(key) {
      storage.removeItem(key);
    },
  };
}
