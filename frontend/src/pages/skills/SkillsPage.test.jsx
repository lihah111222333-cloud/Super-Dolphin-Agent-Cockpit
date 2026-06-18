// @vitest-environment jsdom
import '@testing-library/jest-dom/vitest';
import React from 'react';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import SkillsPage from './SkillsPage.jsx';
import { useProjectStore } from '../../entities/project/model/useProjectStore';

const backend = vi.hoisted(() => ({
  createSkillTool: vi.fn(),
  deleteSkillTool: vi.fn(),
  getSkillTool: vi.fn(),
  listSkillTools: vi.fn(),
  updateSkillTool: vi.fn(),
}));

vi.mock('../../shared/api/backendApi', () => ({
  addProject: vi.fn(),
  createSkillTool: (...args) => backend.createSkillTool(...args),
  deleteSkillTool: (...args) => backend.deleteSkillTool(...args),
  getSkillTool: (...args) => backend.getSkillTool(...args),
  getProjects: vi.fn(),
  listSkillTools: (...args) => backend.listSkillTools(...args),
  registerBridgeLogStore: vi.fn(),
  removeProject: vi.fn(),
  selectProjectDir: vi.fn(),
  sendFrontendLogBatch: vi.fn(),
  setActiveProject: vi.fn(),
  updateSkillTool: (...args) => backend.updateSkillTool(...args),
}));

describe('SkillsPage Skill tool CRUD', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useProjectStore.setState({
      projects: ['/repo/app'],
      active: '/repo/app',
      scopeCwd: '/repo/app',
      showModal: false,
      modalPath: '',
      browsing: false,
    });
    backend.listSkillTools.mockResolvedValue({
      tools: [{
        id: 7,
        methodName: 'backend',
        description: 'Return backend skill details',
        enabled: true,
      }],
    });
    backend.createSkillTool.mockResolvedValue({
      id: 8,
      methodName: 'react_doctor',
      description: 'Return React diagnostics skill details',
      enabled: true,
    });
    backend.getSkillTool.mockResolvedValue({
      id: 7,
      methodName: 'backend',
      description: 'Return backend skill details',
      enabled: true,
    });
    backend.updateSkillTool.mockResolvedValue({
      id: 7,
      methodName: 'backend',
      description: 'Return backend skill details',
      enabled: false,
    });
    backend.deleteSkillTool.mockResolvedValue({ id: 7, deleted: true });
  });

  afterEach(() => {
    cleanup();
  });

  it('renders database Skill tools with enabled status', async () => {
    render(<SkillsPage />);

    const toolCard = await screen.findByText('backend');
    const card = toolCard.closest('.glass-panel');

    expect(within(card).getByText('已启用')).toBeInTheDocument();
    expect(within(card).getByText('Return backend skill details')).toBeInTheDocument();
    expect(backend.listSkillTools).toHaveBeenCalledWith({ cwd: '/repo/app', keyword: '', limit: 200 });
  });

  it('creates, toggles, edits, and deletes database Skill tools', async () => {
    render(<SkillsPage />);
    expect(await screen.findByText('backend')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '新增工具' }));
    const createDialog = await screen.findByRole('dialog', { name: '新增 Skill 工具' });
    fireEvent.change(within(createDialog).getByTestId('skill-tool-method-input'), { target: { value: 'react_doctor' } });
    fireEvent.change(within(createDialog).getByTestId('skill-tool-description-input'), { target: { value: 'Return React diagnostics skill details' } });
    fireEvent.click(within(createDialog).getByTestId('skill-tool-save'));
    await waitFor(() => {
      expect(backend.createSkillTool).toHaveBeenCalledWith({
        cwd: '/repo/app',
        methodName: 'react_doctor',
        description: 'Return React diagnostics skill details',
        enabled: true,
      });
    });

    fireEvent.click(screen.getByTestId('skill-tool-toggle-7'));
    await waitFor(() => {
      expect(backend.updateSkillTool).toHaveBeenCalledWith({
        cwd: '/repo/app',
        id: 7,
        methodName: 'backend',
        description: 'Return backend skill details',
        enabled: false,
      });
    });

    fireEvent.click(screen.getByTestId('skill-tool-edit-7'));
    await waitFor(() => {
      expect(backend.getSkillTool).toHaveBeenCalledWith({ cwd: '/repo/app', id: 7 });
    });
    const editDialog = await screen.findByRole('dialog', { name: '编辑 Skill 工具' });
    fireEvent.change(within(editDialog).getByTestId('skill-tool-method-input'), { target: { value: 'backend_review' } });
    fireEvent.change(within(editDialog).getByTestId('skill-tool-description-input'), { target: { value: 'Return backend review skill details' } });
    fireEvent.click(within(editDialog).getByTestId('skill-tool-save'));
    await waitFor(() => {
      expect(backend.updateSkillTool).toHaveBeenLastCalledWith({
        cwd: '/repo/app',
        id: 7,
        methodName: 'backend_review',
        description: 'Return backend review skill details',
        enabled: true,
      });
    });

    fireEvent.click(screen.getByTestId('skill-tool-delete-7'));
    const deleteDialog = await screen.findByRole('dialog', { name: '删除 Skill 工具' });
    fireEvent.click(within(deleteDialog).getByTestId('skill-tool-delete-confirm'));
    await waitFor(() => {
      expect(backend.deleteSkillTool).toHaveBeenCalledWith({ cwd: '/repo/app', id: 7 });
    });
  });
});
