import { useStore } from 'zustand';
import { createStore } from 'zustand/vanilla';
import { rightPanelWidthSchema } from './shellLayoutSchema.js';

function assertStoragePort(storage) {
  if (
    storage === null
    || typeof storage !== 'object'
    || typeof storage.get !== 'function'
    || typeof storage.set !== 'function'
    || typeof storage.remove !== 'function'
  ) {
    throw new TypeError('Shell layout storage must provide get, set, and remove functions');
  }
}

export function createShellLayoutStore({ storage }) {
  assertStoragePort(storage);

  const storedWidth = storage.get(rightPanelWidthSchema.key);
  let initialRightPanelWidth;
  if (storedWidth === null) {
    initialRightPanelWidth = rightPanelWidthSchema.initialValue;
    storage.set(
      rightPanelWidthSchema.key,
      rightPanelWidthSchema.serialize(initialRightPanelWidth),
    );
  }
  else {
    initialRightPanelWidth = rightPanelWidthSchema.parse(storedWidth);
  }

  return createStore((set) => ({
    rightPanelWidth: initialRightPanelWidth,
    setRightPanelWidth: (nextWidth) => {
      const serializedWidth = rightPanelWidthSchema.serialize(nextWidth);
      storage.set(rightPanelWidthSchema.key, serializedWidth);
      set({ rightPanelWidth: nextWidth });
    },
  }));
}

export function useShellLayoutStore(store, selector) {
  return useStore(store, selector);
}
