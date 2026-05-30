// @vitest-environment jsdom
import { vi, describe, it, expect, beforeEach } from 'vitest';
import { usePreferenceStore } from './usePreferenceStore';
import { useLogStore } from '../../log/model/useLogStore';

const mockBackend = vi.hoisted(() => ({
  getPreference: vi.fn(),
  setPreference: vi.fn(),
  onBridgeEvent: vi.fn(),
  registerBridgeLogStore: vi.fn(),
  sendFrontendLogBatch: vi.fn(),
}));

vi.mock('../../../shared/api/backendApi', () => ({
  getPreference: (...args) => mockBackend.getPreference(...args),
  setPreference: (...args) => mockBackend.setPreference(...args),
  onBridgeEvent: (...args) => mockBackend.onBridgeEvent(...args),
  registerBridgeLogStore: (...args) => mockBackend.registerBridgeLogStore(...args),
  sendFrontendLogBatch: (...args) => mockBackend.sendFrontendLogBatch(...args),
}));

describe('usePreferenceStore', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    usePreferenceStore.getState().destroy();
    usePreferenceStore.setState({
      theme: 'dark',
      chatPrefs: {
        layout: 'standard',
        splitRatio: 0.42,
        threadRailWidth: 320,
      },
      cmdPrefs: {
        layout: 'cards',
        splitRatio: 0.42,
        cardCols: 3,
      },
    });
    useLogStore.setState({
      entries: [],
      bridgeQueue: [],
    });
    document.documentElement.removeAttribute('data-theme');
  });

  describe('initialize', () => {
    it('should load initial preference values from backend', async () => {
      mockBackend.getPreference.mockImplementation(async ({ key }) => {
        if (key === 'viewPrefs.chat') return { layout: 'split', splitRatio: 0.5 };
        if (key === 'viewPrefs.cmd') return { layout: 'compact', cardCols: 4 };
        if (key === 'theme') return 'cyberpunk';
        return null;
      });

      await usePreferenceStore.getState().initialize();

      const state = usePreferenceStore.getState();
      expect(state.chatPrefs).toEqual({ layout: 'split', splitRatio: 0.5 });
      expect(state.cmdPrefs).toEqual({ layout: 'compact', cardCols: 4 });
      expect(state.theme).toBe('cyberpunk');
      expect(document.documentElement.getAttribute('data-theme')).toBe('cyberpunk');
    });

    it('should fall back to default theme dark if theme is missing', async () => {
      mockBackend.getPreference.mockResolvedValue(null);

      await usePreferenceStore.getState().initialize();

      expect(document.documentElement.getAttribute('data-theme')).toBe('dark');
    });

    it('should log error on failure to initialize preferences', async () => {
      mockBackend.getPreference.mockRejectedValue(new Error('Backend offline'));

      await usePreferenceStore.getState().initialize();

      const logState = useLogStore.getState();
      expect(logState.entries.some(e => e.event === 'preferences.init.failed')).toBe(true);
    });
  });

  describe('preference mutations', () => {
    it('should set theme and roll back if backend call fails', async () => {
      mockBackend.setPreference.mockRejectedValue(new Error('Write failed'));

      await usePreferenceStore.getState().setTheme('light');

      // Theme should roll back to 'dark' after backend failure
      const state = usePreferenceStore.getState();
      expect(state.theme).toBe('dark');
      expect(document.documentElement.getAttribute('data-theme')).toBe('dark');

      const logState = useLogStore.getState();
      expect(logState.entries.some(e => e.event === 'preferences.setTheme.failed')).toBe(true);
    });

    it('should successfully set theme and update theme attribute', async () => {
      mockBackend.setPreference.mockResolvedValue({});

      await usePreferenceStore.getState().setTheme('light');

      const state = usePreferenceStore.getState();
      expect(state.theme).toBe('light');
      expect(document.documentElement.getAttribute('data-theme')).toBe('light');
      expect(mockBackend.setPreference).toHaveBeenCalledWith({ key: 'theme', value: 'light' });
    });

    it('should update and roll back chat preferences on failure', async () => {
      mockBackend.setPreference.mockRejectedValue(new Error('Chat pref update failed'));

      await usePreferenceStore.getState().setChatPrefs({ layout: 'split' });

      const state = usePreferenceStore.getState();
      expect(state.chatPrefs.layout).toBe('standard'); // rolled back
    });

    it('should update and roll back cmd preferences on failure', async () => {
      mockBackend.setPreference.mockRejectedValue(new Error('Cmd pref update failed'));

      await usePreferenceStore.getState().setCmdPrefs({ layout: 'list' });

      const state = usePreferenceStore.getState();
      expect(state.cmdPrefs.layout).toBe('cards'); // rolled back
    });
  });

  describe('bridge events sync', () => {
    it('should sync local cache when ui/preferences/changed event is received', async () => {
      let bridgeCallback;
      mockBackend.onBridgeEvent.mockImplementation((cb) => {
        bridgeCallback = cb;
        return vi.fn();
      });

      usePreferenceStore.getState().initialize();

      // Trigger bridge event
      bridgeCallback({
        method: 'ui/preferences/changed',
        payload: {
          key: 'theme',
          value: 'cyberpunk',
        },
      });

      let state = usePreferenceStore.getState();
      expect(state.theme).toBe('cyberpunk');
      expect(document.documentElement.getAttribute('data-theme')).toBe('cyberpunk');

      bridgeCallback({
        method: 'ui/preferences/changed',
        payload: {
          key: 'viewPrefs.chat',
          value: { layout: 'split', splitRatio: 0.8 },
        },
      });

      state = usePreferenceStore.getState();
      expect(state.chatPrefs).toEqual({ layout: 'split', splitRatio: 0.8 });
    });
  });
});
