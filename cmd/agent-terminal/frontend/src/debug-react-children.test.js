import { describe, it, expect } from 'vitest';
import { useThreadStore } from './stores/threads.js';
import { useProjectStore } from './stores/projects.js';
import { useComposerStore } from './stores/composer.js';
import { useVueSetup, val } from './utils/vue-compat.js';
import { UnifiedChatPage } from './pages/UnifiedChatPage.jsx';
import React from 'react';
import { render } from '@testing-library/react';

describe('react children debugger', () => {
  it('debugs computed/refs inside setup return', () => {
    // We want to instantiate UnifiedChatPage.setup and check all returned values
    const projectStore = useProjectStore();
    const threadStore = useThreadStore();
    
    // Add a mock thread
    threadStore.state.threads = [
      { id: 'thread-diff-grouping-1', name: '多文件 Diff 线程', state: 'idle' }
    ];
    threadStore.state.activeThreadId = 'thread-diff-grouping-1';

    const props = {
      projectStore,
      threadStore,
      mode: 'chat',
      windowCwd: '/workspace/project-alpha',
      cwdDisplay: '当前窗口 CWD：/workspace/project-alpha',
    };

    const vm = UnifiedChatPage.setup(props, { emit: () => {} });

    console.log('--- EXPOSED KEYS AND TYPES ---');
    for (const key of Object.keys(vm)) {
      const value = vm[key];
      const unwrapped = val(value);
      console.log(`Key: ${key}, Type of value: ${typeof value}, isRef: ${value && value.__v_isRef}, Unwrapped type: ${typeof unwrapped}`);
      
      // If the unwrapped value is an array, inspect its elements
      if (Array.isArray(unwrapped)) {
        unwrapped.forEach((item, idx) => {
          if (item && typeof item === 'object') {
            console.log(`  Array item ${idx} keys:`, Object.keys(item));
            for (const itemKey of Object.keys(item)) {
              const itemVal = item[itemKey];
              console.log(`    ${itemKey}: type=${typeof itemVal}, isRef=${itemVal && itemVal.__v_isRef}`);
              if (itemVal && typeof itemVal === 'object' && itemVal.__v_isRef) {
                console.log(`    ⚠️ FOUND REF IN ARRAY ITEM! key=${key}, itemKey=${itemKey}`);
              }
            }
          }
        });
      }
      
      // If the unwrapped value is an object, inspect its fields
      if (unwrapped && typeof unwrapped === 'object' && !Array.isArray(unwrapped)) {
        for (const subKey of Object.keys(unwrapped)) {
          const subVal = unwrapped[subKey];
          if (subVal && typeof subVal === 'object' && subVal.__v_isRef) {
            console.log(`  ⚠️ FOUND REF IN OBJECT! key=${key}, subKey=${subKey}`);
          }
        }
      }
    }
  });
});
