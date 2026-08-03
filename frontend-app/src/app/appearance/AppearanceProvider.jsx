import { useEffect, useLayoutEffect } from 'react';
import { useStore } from 'zustand';
import { applyAppearanceToRootTargets } from './appearanceStore.js';

export function AppearanceProvider({ children, store }) {
  if (!store || typeof store.getState !== 'function' || typeof store.subscribe !== 'function') {
    throw new TypeError('AppearanceProvider requires an appearance store');
  }
  if (typeof children !== 'function') {
    throw new TypeError('AppearanceProvider requires a render child');
  }
  const state = useStore(store);
  useLayoutEffect(() => {
    applyAppearanceToRootTargets(document, state);
  }, [state]);

  useEffect(() => {
    if (state.themeMode !== 'system') return undefined;
    const media = window.matchMedia('(prefers-color-scheme: dark)');
    const refresh = () => store.getState().refreshSystemTheme();
    media.addEventListener('change', refresh);
    return () => media.removeEventListener('change', refresh);
  }, [state.themeMode, store]);

  useEffect(() => {
    const reload = (event) => {
      if (event.key === 'super-dolphin.appearance') store.getState().reload();
    };
    window.addEventListener('storage', reload);
    return () => window.removeEventListener('storage', reload);
  }, [store]);

  return children(state);
}

export function AppearanceBootstrapGate({ children, error }) {
  if (error) throw error;
  return children;
}
