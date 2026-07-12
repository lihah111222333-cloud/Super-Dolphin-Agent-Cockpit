import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ComposerCapabilityChips } from './ComposerCapabilityChips.jsx';

const copy = {
  removeCapability: '移除能力',
  staleCapability: '能力已失效，请移除后重新选择',
  unverifiedCapability: '能力目录尚未验证，请等待同步',
};

const skill = {
  kind: 'skill',
  key: 'skill:review',
  name: 'review',
  label: 'Code Review',
  availability: 'ready',
  ref: { name: 'review', scope: 'project', path: '/repo/review' },
};

describe('ComposerCapabilityChips', () => {
  it('renders deduplicated capabilities in insertion order and removes by identity', () => {
    const onRemove = vi.fn();
    const tool = {
      kind: 'mcp_tool',
      key: 'mcp_tool:lsp:grep',
      name: 'grep',
      label: 'LSP grep',
      serverName: 'lsp',
      availability: 'stale',
    };

    render(
      <ComposerCapabilityChips
        items={[skill, tool, { ...skill }]}
        copy={copy}
        onRemove={onRemove}
      />,
    );

    const list = screen.getByRole('list');
    const chips = screen.getAllByRole('listitem');
    expect(list).toContainElement(chips[0]);
    expect(chips).toHaveLength(2);
    expect(chips[0]).toHaveTextContent('Code Review');
    expect(chips[1]).toHaveTextContent('LSP grep');
    expect(chips[1]).toHaveTextContent(copy.staleCapability);
    expect(chips[1]).toHaveAttribute('title', copy.staleCapability);

    const remove = screen.getByRole('button', { name: '移除能力: Code Review' });
    expect(remove).toHaveAttribute('title', '移除能力: Code Review');
    expect(remove).not.toHaveTextContent(/\S/u);
    fireEvent.click(remove);
    expect(onRemove).toHaveBeenCalledWith(skill.key);
  });

  it('exposes an unverified capability through the same visible and tooltip contract', () => {
    render(
      <ComposerCapabilityChips
        items={[{ ...skill, availability: 'unverified' }]}
        copy={copy}
        onRemove={vi.fn()}
      />,
    );

    const chip = screen.getByRole('listitem');
    expect(chip).toHaveTextContent(copy.unverifiedCapability);
    expect(chip).toHaveAttribute('title', copy.unverifiedCapability);
  });

  it('renders nothing when no capabilities are selected', () => {
    const { container } = render(
      <ComposerCapabilityChips items={[]} copy={copy} onRemove={vi.fn()} />,
    );

    expect(container).toBeEmptyDOMElement();
    expect(screen.queryByRole('list')).not.toBeInTheDocument();
  });
});
