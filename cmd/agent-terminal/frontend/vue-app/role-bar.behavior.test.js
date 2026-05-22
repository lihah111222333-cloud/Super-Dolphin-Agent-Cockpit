// @ts-nocheck
import { describe, expect, it, vi } from 'vitest';

import { RoleBar } from './components/RoleBar.js';

function createRoleBar(props) {
  const emitted = [];
  const render = RoleBar.setup(props, {
    emit: (event, payload) => emitted.push([event, payload]),
  });
  return { emitted, render };
}

function walk(vnode, visit) {
  if (!vnode) return;
  visit(vnode);
  const children = Array.isArray(vnode.children) ? vnode.children : [];
  for (const child of children) walk(child, visit);
}

function findVNode(vnode, predicate) {
  let found = null;
  walk(vnode, item => {
    if (!found && predicate(item)) found = item;
  });
  return found;
}

function vnodeText(vnode) {
  if (typeof vnode?.children === 'string') return vnode.children;
  if (!Array.isArray(vnode?.children)) return '';
  return vnode.children.map(child => vnodeText(child)).join('');
}

describe('RoleBar behavior', () => {
  it('does not save roles once disabled', () => {
    const props = {
      roles: [{ key: 'coder', name: '程序员', icon: '💻' }],
      activeKey: 'coder',
      promptCounts: { coder: 1 },
      disabled: false,
    };
    const { emitted, render } = createRoleBar(props);

    const editButton = findVNode(render(), item => item.props?.class === 'sp-role-edit-btn');
    editButton.props.onClick({ stopPropagation: vi.fn() });
    props.disabled = true;

    const saveButton = findVNode(render(), item => vnodeText(item) === '保存');
    saveButton.props.onClick();

    expect(emitted).toEqual([]);
  });

  it('does not delete roles once disabled', () => {
    const props = {
      roles: [{ key: 'coder', name: '程序员', icon: '💻' }],
      activeKey: 'coder',
      promptCounts: { coder: 1 },
      disabled: false,
    };
    const { emitted, render } = createRoleBar(props);

    const editButton = findVNode(render(), item => item.props?.class === 'sp-role-edit-btn');
    editButton.props.onClick({ stopPropagation: vi.fn() });
    props.disabled = true;

    const deleteButton = findVNode(render(), item => vnodeText(item) === '删除角色');
    deleteButton.props.onClick();

    expect(emitted).toEqual([]);
  });
});
