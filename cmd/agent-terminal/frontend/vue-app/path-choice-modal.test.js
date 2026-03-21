// @ts-nocheck
import { describe, expect, it, vi } from 'vitest';

import { reactive, nextTick } from '../lib/vue.esm-browser.prod.js';
import { PathChoiceModal } from './components/PathChoiceModal.js';

function makeProps(overrides = {}) {
  return reactive({
    show: true,
    options: ['/repo/src/a.js', '/repo/lib/src/a.js'],
    title: '选择文件路径',
    truncated: false,
    onConfirm: vi.fn(),
    onCancel: vi.fn(),
    ...overrides,
  });
}

describe('PathChoiceModal', () => {
  it('renders the candidate path list through normalized options', () => {
    const props = makeProps();
    const vm = PathChoiceModal.setup(props);

    expect(vm.normalizedOptions.value).toEqual(['/repo/src/a.js', '/repo/lib/src/a.js']);
    expect(PathChoiceModal.template).toContain('v-for="(option, index) in normalizedOptions"');
    expect(PathChoiceModal.template).toContain('data-testid="path-choice-modal"');
  });

  it('confirms the selected path', () => {
    const props = makeProps();
    const vm = PathChoiceModal.setup(props);

    vm.confirmPath('/repo/lib/src/a.js');

    expect(props.onConfirm).toHaveBeenCalledWith('/repo/lib/src/a.js');
  });

  it('cancels the modal through the cancel action', () => {
    const props = makeProps();
    const vm = PathChoiceModal.setup(props);

    vm.cancelPathChoice();

    expect(props.onCancel).toHaveBeenCalledTimes(1);
  });

  it('shows the truncated hint when the result list was clipped', () => {
    const props = makeProps({ truncated: true });
    const vm = PathChoiceModal.setup(props);

    expect(props.truncated).toBe(true);
    expect(vm.normalizedOptions.value).toHaveLength(2);
    expect(PathChoiceModal.template).toContain('结果已截断，仅显示部分结果');
  });

  it('closes on Escape and focuses itself when opened', async () => {
    const props = makeProps({ show: false });
    const vm = PathChoiceModal.setup(props);
    vm.modalRef.value = { focus: vi.fn() };

    props.show = true;
    await nextTick();
    await nextTick();
    expect(vm.modalRef.value.focus).toHaveBeenCalledTimes(1);

    const event = { preventDefault: vi.fn(), stopPropagation: vi.fn() };
    vm.onEscapeKey(event);
    expect(event.preventDefault).toHaveBeenCalledTimes(1);
    expect(event.stopPropagation).toHaveBeenCalledTimes(1);
    expect(props.onCancel).toHaveBeenCalledTimes(1);
  });

  it('shows empty state message when normalizedOptions is empty', () => {
    const props = makeProps({ options: ['', '  ', null] });
    const vm = PathChoiceModal.setup(props);

    expect(vm.normalizedOptions.value).toEqual([]);
    expect(PathChoiceModal.template).toContain('data-testid="path-choice-empty"');
    expect(PathChoiceModal.template).toContain('没有可选路径');
  });

  it('handles degenerate input: non-array options', () => {
    const props = makeProps({ options: null });
    const vm = PathChoiceModal.setup(props);
    expect(vm.normalizedOptions.value).toEqual([]);
  });

  it('handles degenerate input: options with mixed invalid values', () => {
    const props = makeProps({ options: [undefined, 0, false, '/valid/path.js'] });
    const vm = PathChoiceModal.setup(props);
    expect(vm.normalizedOptions.value).toEqual(['/valid/path.js']);
  });

  it('does not call onConfirm when confirmPath receives empty string', () => {
    const props = makeProps();
    const vm = PathChoiceModal.setup(props);

    vm.confirmPath('');
    vm.confirmPath('   ');
    vm.confirmPath(null);

    expect(props.onConfirm).not.toHaveBeenCalled();
  });
});
