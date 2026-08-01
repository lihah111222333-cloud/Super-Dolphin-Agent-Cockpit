import React from 'react';
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { WorkbenchActivityBar } from './WorkbenchActivityBar.jsx';
import { WorkbenchBottomPanel } from './WorkbenchBottomPanel.jsx';
import { WorkbenchStatusBar } from './WorkbenchStatusBar.jsx';

afterEach(() => cleanup());

function Icon() {
  return <span aria-hidden="true" />;
}

describe('workbench chrome', () => {
  it('exposes selected activity destinations and sidebar toggle', () => {
    const onSelect = vi.fn();
    const onToggleSidebar = vi.fn();
    render(
      <WorkbenchActivityBar
        activePage="chat"
        items={[{ id: 'chat', label: 'Chat', icon: Icon }, { id: 'files', label: 'Files', icon: Icon }]}
        onSelect={onSelect}
        onToggleSidebar={onToggleSidebar}
        sidebarOpen
      />,
    );
    expect(screen.getByRole('navigation', { name: '工作台活动栏' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Chat' })).toHaveAttribute('aria-current', 'page');
    fireEvent.click(screen.getByRole('button', { name: 'Files' }));
    fireEvent.click(screen.getByRole('button', { name: '收起主侧栏' }));
    expect(onSelect).toHaveBeenCalledWith('files');
    expect(onToggleSidebar).toHaveBeenCalled();
  });

  it('keeps terminal unmistakably demo-only and bottom panel adjustable', () => {
    const onHeightChange = vi.fn();
    render(<WorkbenchBottomPanel activePage="chat" onHeightChange={onHeightChange} projectPath="/workspace" rightPanelOpen />);
    expect(screen.getByRole('region', { name: '底部工作台' })).toHaveStyle({ '--workbench-bottom-height': '36px' });
    expect(onHeightChange).toHaveBeenLastCalledWith(36);
    fireEvent.click(screen.getByRole('button', { name: '展开底部面板' }));
    expect(onHeightChange).toHaveBeenLastCalledWith(188);
    fireEvent.click(screen.getByRole('tab', { name: /Terminal/ }));
    expect(screen.getByText('Demo · 只读')).toBeInTheDocument();
    expect(screen.getByText(/不会执行命令/)).toBeInTheDocument();
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '收起底部面板' }));
    expect(screen.getByRole('region', { name: '底部工作台' })).toHaveClass('is-collapsed');
    expect(onHeightChange).toHaveBeenLastCalledWith(36);
  });

  it('keeps the bottom workbench out of non-chat pages', () => {
    render(<WorkbenchBottomPanel activePage="observability" projectPath="/workspace" rightPanelOpen={false} />);

    expect(screen.queryByRole('region', { name: '底部工作台' })).not.toBeInTheDocument();
  });

  it('renders real workspace and appearance status', () => {
    render(
      <WorkbenchStatusBar
        accent="mint"
        activePage="chat"
        projectPath="/workspace"
        rightPanelOpen
        themeMode="dark"
        uiScale={125}
      />,
    );
    expect(screen.getByRole('contentinfo', { name: '工作台状态' })).toHaveTextContent('/workspace');
    expect(screen.getByText(/dark · 125% · mint/)).toBeInTheDocument();
  });
});
