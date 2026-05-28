// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { createRenderer, h, nextTick, reactive, ref } from '../lib/vue.esm-browser.prod.js';

const detailMock = vi.hoisted(() => ({
  state: {
    loading: false,
    error: null,
    runsError: null,
    show: false,
    dag: null,
    nodes: [],
    runs: [],
    activeRun: null,
    run: null,
    selectedRunKey: '',
    finalOutput: null,
    starting: false,
    startError: null,
    terminating: false,
    terminateError: null,
    terminateWarning: null,
    savingNodeKey: '',
    saveError: null,
  },
  open: vi.fn(),
  start: vi.fn(),
  terminateActiveRun: vi.fn(),
  selectRun: vi.fn(),
  saveAgentNode: vi.fn(),
  handleStatusEvent: vi.fn(),
}));

vi.mock('./composables/useDagDetail.js', () => ({
  useDagDetail: () => detailMock,
}));
vi.mock('./services/api.js', () => ({
  callAPI: vi.fn(async () => null),
}));

import { DagsPage } from './pages/DagsPage.js';
import { requireDagStatusEventPayload, useDagStatusEventBridge } from './composables/useDagStatusEventBridge.js';

function mountNoop(component) {
  const renderer = createRenderer({
    patchProp() {},
    insert(child, parent) { parent.children = parent.children || []; parent.children.push(child); },
    remove() {},
    createElement(type) { return { type, children: [] }; },
    createText(text) { return { text }; },
    createComment(text) { return { comment: text }; },
    setText(node, text) { node.text = text; },
    setElementText(node, text) { node.text = text; },
    parentNode() { return null; },
    nextSibling() { return null; },
  });
  const root = { children: [] };
  renderer.createApp(component).mount(root);
}

beforeEach(() => {
  detailMock.open.mockReset();
  detailMock.handleStatusEvent.mockReset();
});

describe('DagsPage node status bridge events', () => {
  it('forwards bridge node status events to the open DAG detail state', async () => {
    const props = reactive({
      items: [{ dag_key: 'dag-a', title: 'Dag A' }],
      emptyText: '暂无 DAG',
      statusEvents: [],
    });

    DagsPage.setup(props, { emit: vi.fn() });
    const payload = { dag_key: 'dag-a', run_key: 'run-1', node_key: 'report', new_status: 'done' };
    props.statusEvents = [{ seq: 1, payload }];
    await nextTick();

    expect(detailMock.handleStatusEvent).toHaveBeenCalledWith(payload);
  });

  it('does not coalesce multiple same-tick node status events', () => {
    const props = reactive({
      items: [{ dag_key: 'dag-a', title: 'Dag A' }],
      emptyText: '暂无 DAG',
      statusEvents: [],
    });

    DagsPage.setup(props, { emit: vi.fn() });
    const first = { dag_key: 'dag-a', run_key: 'run-1', node_key: 'draft', new_status: 'running' };
    const second = { dag_key: 'dag-a', run_key: 'run-1', node_key: 'report', new_status: 'done' };
    props.statusEvents = [{ seq: 1, payload: first }, { seq: 2, payload: second }];

    expect(detailMock.handleStatusEvent).toHaveBeenNthCalledWith(1, first);
    expect(detailMock.handleStatusEvent).toHaveBeenNthCalledWith(2, second);
  });

  it('does not lose same-tick events across the AppRoot to DagsPage render boundary', async () => {
    const statusEvents = ref([]);
    const BridgeChild = {
      props: { statusEvents: { type: Array, default: () => [] } },
      setup(props) {
        useDagStatusEventBridge(props, detailMock);
        return () => h('div');
      },
    };
    mountNoop({
      setup: () => () => h(BridgeChild, { statusEvents: statusEvents.value }),
    });

    const first = { dag_key: 'dag-a', run_key: 'run-1', node_key: 'draft', new_status: 'running' };
    const second = { dag_key: 'dag-a', run_key: 'run-1', node_key: 'report', new_status: 'done' };
    statusEvents.value = [{ seq: 1, payload: first }];
    statusEvents.value = [...statusEvents.value, { seq: 2, payload: second }];
    await nextTick();

    expect(detailMock.handleStatusEvent).toHaveBeenNthCalledWith(1, first);
    expect(detailMock.handleStatusEvent).toHaveBeenNthCalledWith(2, second);
  });

  it('fails fast when a bridge node status event has no payload', () => {
    expect(() => requireDagStatusEventPayload({ seq: 1 })).toThrow('dag status event payload is required');
  });

  it('fails fast when a bridge node status event has no node key or status', () => {
    expect(() => requireDagStatusEventPayload({ seq: 1, payload: { dag_key: 'dag-a', run_key: 'run-1', new_status: 'running' } })).toThrow('dag status event node key is required');
    expect(() => requireDagStatusEventPayload({ seq: 2, payload: { dag_key: 'dag-a', run_key: 'run-1', node_key: 'draft' } })).toThrow('dag status event status is required');
    expect(() => requireDagStatusEventPayload({ seq: 3, payload: { dag_key: 'dag-a', node_key: 'draft', new_status: 'running' } })).toThrow('dag status event run identity is required');
  });

  it('rejects non-contract status event field aliases', () => {
    expect(() => requireDagStatusEventPayload({ seq: 1, payload: { dagKey: 'dag-a', run_key: 'run-1', node_key: 'draft', new_status: 'running' } })).toThrow('dag status event dag key is required');
    expect(() => requireDagStatusEventPayload({ seq: 2, payload: { dag_key: 'dag-a', runKey: 'run-1', node_key: 'draft', new_status: 'running' } })).toThrow('dag status event run identity is required');
    expect(() => requireDagStatusEventPayload({ seq: 3, payload: { dag_key: 'dag-a', run_key: 'run-1', nodeKey: 'draft', new_status: 'running' } })).toThrow('dag status event node key is required');
    expect(() => requireDagStatusEventPayload({ seq: 4, payload: { dag_key: 'dag-a', run_key: 'run-1', node_key: 'draft', status: 'running' } })).toThrow('dag status event status is required');
  });

  it('fails fast when the DAG detail status handler is missing', () => {
    expect(() => {
      useDagStatusEventBridge(reactive({ statusEvents: [] }), {});
    }).toThrow('dag status event handler is required');
  });
});
