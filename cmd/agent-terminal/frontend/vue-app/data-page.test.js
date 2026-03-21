// @ts-nocheck
import { describe, expect, it, vi } from 'vitest';
import { nextTick, reactive } from '../lib/vue.esm-browser.prod.js';

const logMock = vi.hoisted(() => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
}));

vi.mock('./services/log.js', () => ({
  logDebug: logMock.logDebug,
  logInfo: logMock.logInfo,
}));

import { DataPage } from './pages/DataPage.js';

describe('DataPage', () => {
  it('exports the page component contract', () => {
    expect(DataPage.name).toBe('DataPage');
    expect(typeof DataPage.setup).toBe('function');
    expect(DataPage.template).toContain("items.length === 0");
    expect(DataPage.template).toContain("data-list-vue");
  });

  it('logs item-count changes when props.items length changes', async () => {
    logMock.logDebug.mockReset();
    const props = reactive({
      pageId: 'agents',
      title: 'Agents',
      icon: 'A',
      items: [],
      emptyText: '暂无数据',
      fields: [],
    });

    const vm = DataPage.setup(props);
    expect(vm).toEqual({});

    props.items.push({ id: 'agent-1' });
    await nextTick();

    expect(logMock.logDebug).toHaveBeenCalledWith('page', 'data.items.changed', {
      page: 'agents',
      count: 1,
    });
  });
});
