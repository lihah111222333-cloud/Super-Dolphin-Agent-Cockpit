import { describe, expect, it } from 'vitest';
import { SettingsPage } from './SettingsPage.jsx';

describe('SettingsPage module', () => {
  it('exports the settings page component', () => {
    expect(SettingsPage).toBeTypeOf('function');
  });
});
