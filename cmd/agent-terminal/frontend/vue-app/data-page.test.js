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
    expect(typeof vm.onCardClick).toBe('function');

    props.items.push({ id: 'agent-1' });
    await nextTick();

    expect(logMock.logDebug).toHaveBeenCalledWith('page', 'data.items.changed', {
      page: 'agents',
      count: 1,
    });
  });

  it('emits select when a card is clicked and clickable is true', () => {
    const props = reactive({
      pageId: 'dags',
      title: 'DAG',
      icon: 'D',
      items: [],
      emptyText: '暂无',
      fields: [],
      clickable: true,
    });
    const emit = vi.fn();
    const vm = DataPage.setup(props, { emit });
    const item = { dag_key: 'dag-1' };
    vm.onCardClick(item);
    expect(emit).toHaveBeenCalledWith('select', item);
  });

  it('does not emit select when clickable is false', () => {
    const props = reactive({
      pageId: 'dags',
      title: 'DAG',
      icon: 'D',
      items: [],
      emptyText: '暂无',
      fields: [],
      clickable: false,
    });
    const emit = vi.fn();
    const vm = DataPage.setup(props, { emit });
    vm.onCardClick({ dag_key: 'dag-1' });
    expect(emit).not.toHaveBeenCalled();
  });
});
