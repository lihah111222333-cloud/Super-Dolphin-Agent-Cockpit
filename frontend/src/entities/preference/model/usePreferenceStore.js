// Preferences and Layout Zustand Store
import { create } from 'zustand';
import { getPreference, onBridgeEvent, setPreference } from '../../../shared/api/backendApi';
import { useLogStore } from '../../log/model/useLogStore';

const PREFERENCES_BRIDGE_METHOD = 'ui/preferences/changed';

export const usePreferenceStore = create((set, get) => {
  let bridgeUnsubscribe = null;

  // Listen to preferences changed events from backend
  const attachBridge = () => {
    if (bridgeUnsubscribe) return;
    try {
      bridgeUnsubscribe = onBridgeEvent((evt) => {
        const type = (evt?.type || evt?.method || '').toString();
        if (type !== PREFERENCES_BRIDGE_METHOD) return;
        const payload = evt?.payload || evt?.params || {};
        const key = (payload.key || '').toString();
        if (!key) return;

        // Sync local cache
        if (key === 'viewPrefs.chat') {
          set({ chatPrefs: payload.value });
        } else if (key === 'viewPrefs.cmd') {
          set({ cmdPrefs: payload.value });
        } else if (key === 'theme') {
          set({ theme: payload.value });
          document.documentElement.setAttribute('data-theme', payload.value);
        }
      });
    } catch (error) {
      useLogStore.getState().warn('preferences.bridge.attach.failed', { error: error.message });
    }
  };

  return {
    theme: 'dark', // default theme
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

    initialize: async () => {
      attachBridge();
      try {
        // Load initial values from backend
        const chatValue = await getPreference({ key: 'viewPrefs.chat' });
        if (chatValue) set({ chatPrefs: chatValue });

        const cmdValue = await getPreference({ key: 'viewPrefs.cmd' });
        if (cmdValue) set({ cmdPrefs: cmdValue });

        const themeValue = await getPreference({ key: 'theme' });
        if (themeValue) {
          set({ theme: themeValue });
          document.documentElement.setAttribute('data-theme', themeValue);
        } else {
          document.documentElement.setAttribute('data-theme', 'dark');
        }
      } catch (error) {
        useLogStore.getState().error('preferences.init.failed', { error: error.message });
      }
    },

    setTheme: async (nextTheme) => {
      const prevTheme = get().theme;
      set({ theme: nextTheme });
      document.documentElement.setAttribute('data-theme', nextTheme);
      try {
        await setPreference({ key: 'theme', value: nextTheme });
      } catch (error) {
        set({ theme: prevTheme });
        document.documentElement.setAttribute('data-theme', prevTheme);
        useLogStore.getState().error('preferences.setTheme.failed', { error: error.message });
      }
    },

    setChatPrefs: async (patch) => {
      const nextPrefs = { ...get().chatPrefs, ...patch };
      const prevPrefs = get().chatPrefs;
      set({ chatPrefs: nextPrefs });
      try {
        await setPreference({ key: 'viewPrefs.chat', value: nextPrefs });
      } catch (error) {
        set({ chatPrefs: prevPrefs });
        useLogStore.getState().error('preferences.setChatPrefs.failed', { error: error.message });
      }
    },

    setCmdPrefs: async (patch) => {
      const nextPrefs = { ...get().cmdPrefs, ...patch };
      const prevPrefs = get().cmdPrefs;
      set({ cmdPrefs: nextPrefs });
      try {
        await setPreference({ key: 'viewPrefs.cmd', value: nextPrefs });
      } catch (error) {
        set({ cmdPrefs: prevPrefs });
        useLogStore.getState().error('preferences.setCmdPrefs.failed', { error: error.message });
      }
    },

    destroy: () => {
      if (typeof bridgeUnsubscribe === 'function') {
        bridgeUnsubscribe();
      }
      bridgeUnsubscribe = null;
    }
  };
});
