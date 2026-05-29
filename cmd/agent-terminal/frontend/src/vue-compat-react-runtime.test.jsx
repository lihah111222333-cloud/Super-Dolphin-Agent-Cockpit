import React, { useEffect } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { computed, reactive, ref, watch, onMounted, onBeforeUnmount } from '../lib/vue.esm-browser.prod.js';
import { useVueSetup } from './utils/vue-compat.js';
import { useProjectStore, projectStoreVanilla } from './stores/projects.js';
import { useThreadStore, threadStoreVanilla } from './stores/threads.js';

afterEach(() => {
  cleanup();
  projectStoreVanilla.setState({
    projects: [],
    active: '.',
    scopeCwd: '',
    showModal: false,
    modalPath: '',
    browsing: false,
  });
  threadStoreVanilla.setState({
    activeThreadId: '',
    activeCmdThreadId: '',
    threads: [],
  });
  delete window.__VUE_SETUP_ACTIVE__;
  delete window.__VUE_COMPAT_MOUNTED_HOOKS__;
  delete window.__VUE_COMPAT_UNMOUNT_HOOKS__;
  delete window.__VUE_ON_MOUNTED__;
  delete window.__VUE_ON_BEFORE_UNMOUNT__;
});

function lifecycleSetup(props) {
  const count = ref(0);
  onMounted(() => {
    count.value += 1;
    props.onMount?.();
  });
  onBeforeUnmount(() => props.onUnmount?.());
  return { count };
}

function LifecycleHarness(props) {
  const vm = useVueSetup(lifecycleSetup, props, () => {});
  return <div data-testid="mounted-count">{vm.count.value}</div>;
}

function rerenderSetup() {
  rerenderSetup.calls += 1;
  const count = ref(0);
  return {
    count,
    increment() {
      count.value += 1;
    },
  };
}
rerenderSetup.calls = 0;

function RerenderHarness() {
  const vm = useVueSetup(rerenderSetup, {}, () => {});
  return (
    <button type="button" data-testid="increment" onClick={vm.increment}>
      {vm.count.value}
    </button>
  );
}

function reactiveSetup() {
  const form = reactive({ name: '' });
  return {
    form,
    setName() {
      form.name = 'Ada';
    },
  };
}

function ReactiveHarness() {
  const vm = useVueSetup(reactiveSetup, {}, () => {});
  return (
    <div>
      <span data-testid="reactive-name">{vm.form.name}</span>
      <button type="button" data-testid="set-name" onClick={vm.setName}>set</button>
    </div>
  );
}

function highFrequencySetup() {
  const source = ref(0);
  const threads = computed(() => [
    { id: 'thread-a', seq: source.value },
    { id: 'thread-b', seq: source.value },
  ]);
  const chatThreadOptions = computed(() => threads.value);
  const visibleChatThreadCards = computed(() => chatThreadOptions.value.map((thread) => ({
    id: thread.id,
    seq: thread.seq,
  })));

  return {
    source,
    threads,
    chatThreadOptions,
    visibleChatThreadCards,
    bump() {
      source.value += 1;
    },
  };
}

function HighFrequencyHarness({ onCapture }) {
  const vm = useVueSetup(highFrequencySetup, {}, () => {});

  useEffect(() => {
    onCapture(vm);
  }, [onCapture, vm]);

  const cards = vm.visibleChatThreadCards.value;
  return <span data-testid="high-frequency-seq">{cards[0]?.seq ?? 0}</span>;
}

function StoreCaptureHarness({ onCapture }) {
  const projectStore = useProjectStore();
  const threadStore = useThreadStore();

  useEffect(() => {
    onCapture({ projectStore, threadStore });
  }, []);

  return (
    <div>
      <span data-testid="project-active">{projectStore.state.active}</span>
      <span data-testid="thread-active">{threadStore.state.activeThreadId}</span>
    </div>
  );
}

describe('Vue compat React runtime', () => {
  it('captures legacy lifecycle hooks and replays them through React mount/unmount', () => {
    const onMount = vi.fn();
    const onUnmount = vi.fn();

    const view = render(<LifecycleHarness onMount={onMount} onUnmount={onUnmount} />);

    expect(onMount).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId('mounted-count').textContent).toBe('1');

    view.unmount();

    expect(onUnmount).toHaveBeenCalledTimes(1);
  });

  it('does not rerun setup when an inline emit callback changes during a ref update', () => {
    rerenderSetup.calls = 0;

    render(<RerenderHarness />);
    fireEvent.click(screen.getByTestId('increment'));

    expect(rerenderSetup.calls).toBe(1);
    expect(screen.getByTestId('increment').textContent).toBe('1');
  });

  it('rerenders when setup returns a reactive object', () => {
    render(<ReactiveHarness />);

    fireEvent.click(screen.getByTestId('set-name'));

    expect(screen.getByTestId('reactive-name').textContent).toBe('Ada');
  });

  it('does not treat high-frequency derived Vue updates as an infinite loop', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => {});
    let capturedVm;

    render(<HighFrequencyHarness onCapture={(vm) => { capturedVm = vm; }} />);

    expect(() => {
      act(() => {
        for (let i = 0; i < 120; i += 1) {
          capturedVm.bump();
        }
      });
    }).not.toThrow();

    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 25));
    });

    expect(screen.getByTestId('high-frequency-seq').textContent).toBe('120');
    expect(consoleError.mock.calls.some(([message]) => (
      message?.toString().includes('[useVueSetup] Infinite loop detected')
    ))).toBe(false);

    consoleError.mockRestore();
  });

  it('stops watchers created inside setup when the React wrapper unmounts', () => {
    let source;
    let observed = 0;
    function watcherSetup() {
      source = ref(0);
      watch(source, () => {
        observed += 1;
      }, { flush: 'sync' });
      return { source };
    }
    function WatchHarness() {
      useVueSetup(watcherSetup, {}, () => {});
      return null;
    }

    const view = render(<WatchHarness />);
    act(() => {
      source.value = 1;
    });
    expect(observed).toBe(1);

    view.unmount();
    act(() => {
      source.value = 2;
    });

    expect(observed).toBe(1);
  });

  it('keeps captured project and thread store objects reading live state', () => {
    let captured;
    render(<StoreCaptureHarness onCapture={(stores) => { captured = stores; }} />);

    act(() => {
      projectStoreVanilla.setState({ projects: ['/next'], active: '/next' });
      threadStoreVanilla.setState({ activeThreadId: 'thread-2' });
    });

    expect(screen.getByTestId('project-active').textContent).toBe('/next');
    expect(screen.getByTestId('thread-active').textContent).toBe('thread-2');
    expect(captured.projectStore.state.active).toBe('/next');
    expect(captured.threadStore.state.activeThreadId).toBe('thread-2');
  });
});
