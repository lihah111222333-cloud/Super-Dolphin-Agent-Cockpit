import React, { useState } from 'react';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { AppearanceSettingsPanel } from './AppearanceSettingsPanel.jsx';

afterEach(() => {
  cleanup();
});

function Harness({ onState }) {
  const [settings, setSettings] = useState({ themeMode: 'system', uiScale: 100, accent: 'violet' });
  onState(settings);
  const appearance = {
    ...settings,
    reset: () => setSettings({ themeMode: 'system', uiScale: 100, accent: 'violet' }),
    setAccent: (accent) => setSettings((current) => ({ ...current, accent })),
    setThemeMode: (themeMode) => setSettings((current) => ({ ...current, themeMode })),
    setUiScale: (uiScale) => setSettings((current) => ({ ...current, uiScale })),
  };
  return <AppearanceSettingsPanel appearance={appearance} />;
}

function renderPanel() {
  let latest;
  render(<Harness onState={(state) => { latest = state; }} />);
  return {
    panel: screen.getByTestId('appearance-settings-panel'),
    state: () => latest,
  };
}

describe('AppearanceSettingsPanel', () => {
  it('starts with system theme, 100% zoom, and a live preview', () => {
    const { panel } = renderPanel();
    const preview = screen.getByTestId('appearance-preview');

    expect(within(panel).getByRole('radio', { name: /跟随系统/ })).toBeChecked();
    expect(within(panel).getByRole('radio', { name: '100%' })).toBeChecked();
    [80, 90, 100, 110, 125, 150].forEach((level) => {
      expect(within(panel).getByRole('radio', { name: `${level}%` })).toBeEnabled();
    });
    expect(preview).toHaveAttribute('data-theme', 'system');
    expect(preview).toHaveAttribute('data-zoom', '100');
    expect(preview).toHaveAttribute('aria-live', 'polite');
    expect(within(preview).getByText('准备好继续创作了吗？')).toBeInTheDocument();
  });

  it('updates the global controller and preview when choices change', () => {
    const { panel, state } = renderPanel();
    const preview = screen.getByTestId('appearance-preview');

    fireEvent.click(within(panel).getByRole('radio', { name: /深色/ }));
    fireEvent.click(within(panel).getByRole('radio', { name: '125%' }));

    expect(preview).toHaveAttribute('data-theme', 'dark');
    expect(preview).toHaveAttribute('data-zoom', '125');
    expect(within(preview).getByText('深色')).toBeInTheDocument();
    expect(within(preview).getByText('125%')).toBeInTheDocument();
    expect(state()).toMatchObject({ themeMode: 'dark', uiScale: 125 });
  });

  it('updates the selected accent and preview color', () => {
    const { panel } = renderPanel();
    const preview = screen.getByTestId('appearance-preview');
    const mint = within(panel).getByRole('radio', { name: '薄荷绿' });

    fireEvent.click(mint);

    expect(mint).toBeChecked();
    expect(preview).toHaveAttribute('data-accent', 'mint');
    expect(preview).toHaveStyle({ '--appearance-accent': '#21a179' });
    expect(within(preview).getByText('薄荷绿')).toBeInTheDocument();
  });

  it('keeps unavailable accents disabled', () => {
    const { panel } = renderPanel();
    const disabledAccent = within(panel).getByRole('radio', { name: '自定义色（即将推出）' });

    expect(disabledAccent).toBeDisabled();
    expect(screen.getByTestId('appearance-preview')).toHaveAttribute('data-accent', 'violet');
  });

  it('resets the persisted global choices explicitly', () => {
    const { panel, state } = renderPanel();
    fireEvent.click(within(panel).getByRole('radio', { name: /深色/ }));
    fireEvent.click(within(panel).getByRole('radio', { name: '125%' }));
    fireEvent.click(within(panel).getByRole('radio', { name: '薄荷绿' }));
    fireEvent.click(within(panel).getByRole('button', { name: '恢复默认' }));
    expect(state()).toMatchObject({ themeMode: 'system', uiScale: 100, accent: 'violet' });
  });

  it('exposes named groups and responsive layout structure', () => {
    const { panel } = renderPanel();

    expect(within(panel).getByRole('group', { name: '主题' })).toBeInTheDocument();
    expect(within(panel).getByRole('group', { name: '界面缩放' })).toBeInTheDocument();
    expect(within(panel).getByRole('group', { name: '强调色' })).toBeInTheDocument();
    expect(screen.getByRole('complementary', { name: '外观实时预览' })).toBeInTheDocument();
    expect(screen.getByTestId('appearance-responsive-layout')).toHaveClass('appearance-settings__layout');
    expect(screen.getByTestId('appearance-responsive-layout').children).toHaveLength(2);
  });
});
