import React from 'react';
import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { applyAppearanceToElement, createAppearanceStore } from './appearanceStore.js';
import { AppearanceProvider } from './AppearanceProvider.jsx';

afterEach(() => cleanup());

function storage() {
  const values = new Map();
  return {
    get: (key) => values.get(key) ?? null,
    remove: (key) => values.delete(key),
    set: (key, value) => values.set(key, value),
  };
}

function Probe({ state }) {
  return <output data-testid="appearance-probe">{state.themeMode}/{state.uiScale}/{state.accent}</output>;
}

describe('AppearanceProvider', () => {
  it('projects global state and exposes the single store', () => {
    const media = {
      matches: false,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    };
    vi.stubGlobal('matchMedia', vi.fn(() => media));
    const store = createAppearanceStore({ matchMedia: window.matchMedia, storage: storage() });
    render(<AppearanceProvider store={store}>{(state) => <Probe state={state} />}</AppearanceProvider>);
    expect(screen.getByTestId('appearance-probe')).toHaveTextContent('system/100/violet');
    expect(document.documentElement).toHaveAttribute('data-theme', 'light');
    expect(document.body).toHaveAttribute('data-accent', 'violet');
    expect(media.addEventListener).toHaveBeenCalledWith('change', expect.any(Function));
    vi.unstubAllGlobals();
  });

  it('applies all attributes and scale variables to overlay targets', () => {
    const target = document.createElement('div');
    applyAppearanceToElement(target, {
      themeMode: 'dark',
      resolvedTheme: 'dark',
      uiScale: 125,
      accent: 'mint',
    });
    expect(target).toHaveAttribute('data-theme', 'dark');
    expect(target).toHaveAttribute('data-theme-mode', 'dark');
    expect(target).toHaveAttribute('data-ui-scale', '125');
    expect(target).toHaveAttribute('data-accent', 'mint');
    expect(target.style.getPropertyValue('--ui-scale')).toBe('1.25');
  });
});
