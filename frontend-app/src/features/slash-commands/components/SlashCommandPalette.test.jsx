import { fireEvent, render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { SlashCommandPalette } from './SlashCommandPalette.jsx';

const copy = {
  ariaLabel: '命令与能力',
  categories: {
    builtin: '快捷命令',
    skill: '技能',
    prompt: '提示词',
    automation: '自动化',
    mcp_tool: 'MCP 工具',
  },
  loading: '正在加载',
  loadError: '加载失败',
  projectRequired: '请先选择项目',
  noResults: '没有匹配的命令',
  selecting: '正在应用命令',
};

const builtin = {
  id: 'builtin:new',
  kind: 'builtin',
  name: 'new',
  label: '新建对话',
  description: '新建一个对话',
  keywords: ['new'],
  payload: { action: 'new' },
  disabled: false,
  disabledReason: '',
};

const enabledSkill = {
  id: 'skill:review',
  kind: 'skill',
  name: 'review',
  label: 'Code Review',
  description: 'Review code',
  keywords: ['review'],
  payload: { capability: { kind: 'skill', key: 'skill:review', name: 'review' } },
  disabled: false,
  disabledReason: '',
};

const disabledSkill = {
  ...enabledSkill,
  id: 'skill:disabled',
  name: 'disabled',
  label: 'Disabled Skill',
  disabled: true,
  disabledReason: 'Unavailable',
};

function categoryStates(overrides = {}) {
  return {
    builtin: { status: 'success', error: '' },
    skill: { status: 'success', error: '' },
    prompt: { status: 'success', error: '' },
    automation: { status: 'success', error: '' },
    mcp_tool: { status: 'success', error: '' },
    ...overrides,
  };
}

function renderPalette(overrides = {}) {
  const props = {
    activeId: builtin.id,
    categoryStates: categoryStates(),
    copy,
    cwd: '/repo',
    items: [builtin, enabledSkill, disabledSkill],
    listboxId: 'slash-listbox',
    open: true,
    selectItem: vi.fn(),
    selecting: false,
    setActiveId: vi.fn(),
    ...overrides,
  };
  render(<SlashCommandPalette {...props} />);
  return props;
}

it('supports pointer activation and blocks disabled option selection', () => {
  const props = renderPalette();
  const lastEnabled = screen.getByRole('option', { name: /Code Review/ });

  fireEvent.mouseEnter(lastEnabled);
  expect(props.setActiveId).toHaveBeenCalledWith(enabledSkill.id);
  fireEvent.click(lastEnabled);
  expect(props.selectItem).toHaveBeenCalledWith(enabledSkill);

  fireEvent.click(screen.getByRole('option', { name: /Disabled Skill/ }));
  expect(props.selectItem).toHaveBeenCalledTimes(1);
  expect(screen.getByRole('option', { name: /Disabled Skill/ })).toHaveAttribute(
    'aria-disabled',
    'true',
  );
});

it('keeps successful built-ins visible beside a failed category', () => {
  renderPalette({
    items: [builtin],
    categoryStates: categoryStates({
      skill: { status: 'error', error: 'skill catalog failed' },
    }),
  });

  expect(screen.getByRole('option', { name: /新建对话/ })).toBeVisible();
  expect(screen.getAllByText(/skill catalog failed/)).toHaveLength(2);
  expect(screen.getByText('技能')).toBeVisible();
  expect(screen.getByRole('status')).toHaveTextContent('加载失败: skill catalog failed');
});

it('renders project-required and successful-empty states explicitly', () => {
  const { rerender } = render(
    <SlashCommandPalette
      activeId=""
      categoryStates={categoryStates({
        skill: { status: 'disabled', error: '' },
        prompt: { status: 'disabled', error: '' },
        automation: { status: 'disabled', error: '' },
        mcp_tool: { status: 'disabled', error: '' },
      })}
      copy={copy}
      cwd=""
      items={[]}
      listboxId="slash-listbox"
      open
      selectItem={vi.fn()}
      selecting={false}
      setActiveId={vi.fn()}
    />,
  );

  expect(screen.getAllByText('请先选择项目').length).toBeGreaterThan(0);

  rerender(
    <SlashCommandPalette
      activeId=""
      categoryStates={categoryStates()}
      copy={copy}
      cwd="/repo"
      items={[]}
      listboxId="slash-listbox"
      open
      selectItem={vi.fn()}
      selecting={false}
      setActiveId={vi.fn()}
    />,
  );
  expect(screen.getByText('没有匹配的命令')).toBeVisible();
});

it('does not render when closed', () => {
  renderPalette({ open: false });
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
});
