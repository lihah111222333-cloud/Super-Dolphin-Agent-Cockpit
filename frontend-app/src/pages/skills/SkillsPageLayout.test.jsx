import { describe, expect, it } from 'vitest';
import { fireEvent, screen, within } from '@testing-library/react';
import { backend, renderSkillsPage } from './SkillsPageTestSupport.jsx';

describe('SkillsPage backend migration: layout', () => {
  it('uses unified layout containers and compact controls across tabs', async () => {
    const { container } = renderSkillsPage();

    // 1. Plugins Tab (default)
    expect(container.querySelector('.plugins-square-container')).toBeInTheDocument();

    // 2. Library Tab
    fireEvent.click(screen.getByRole('button', { name: '技能库' }));

    const libraryContainer = container.querySelector('.plugins-square-container');
    expect(libraryContainer).toBeInTheDocument();

    // Assert Left-aligned Page Header
    const header = libraryContainer.querySelector('.plugins-square-header');
    expect(header).toBeInTheDocument();
    expect(within(header).getByRole('heading', { name: '插件与技能', level: 1 })).toBeInTheDocument();
    expect(header.querySelector('.plugins-square-subtitle')).toBeInTheDocument();

    // Assert Compact Stats Overview structure
    const overview = libraryContainer.querySelector('.skills-overview-compact');
    expect(overview).toBeInTheDocument();
    expect(overview.querySelector('.overview-summary-text')).not.toBeInTheDocument();
    const statsRow = overview.querySelector('.overview-stats-row');
    expect(statsRow).toBeInTheDocument();
    expect(statsRow.tagName.toLowerCase()).toBe('dl');
    expect(statsRow.querySelectorAll('div').length).toBe(4);

    // Assert Unified Toolbar Controls
    const toolbar = libraryContainer.querySelector('.skills-toolbar.skills-toolbar-unified');
    expect(toolbar).toBeInTheDocument();
    expect(within(toolbar).getByPlaceholderText('搜索技能名称、简介、关键词...')).toBeInTheDocument();

    const actionsContainer = toolbar.querySelector('.skills-toolbar-actions');
    expect(actionsContainer).toBeInTheDocument();

    const importBtn = within(actionsContainer).getByRole('button', { name: '批量导入技能目录' });
    expect(importBtn).toBeInTheDocument();
    expect(importBtn.classList.contains('btn-secondary')).toBe(true);

    const newBtn = within(actionsContainer).getByRole('button', { name: '新建技能' });
    expect(newBtn).toBeInTheDocument();
    expect(newBtn.classList.contains('btn-primary')).toBe(true);

    // Assert Skill Card (using mocked project data)
    await screen.findByRole('heading', { name: '后端' });
    const skillCard = libraryContainer.querySelector('.skill-card.skill-card-redesign');
    expect(skillCard).toBeInTheDocument();
    expect(within(skillCard).getByRole('heading', { name: '后端', level: 3 })).toBeInTheDocument();
    expect(skillCard.querySelector('.path-text')).toBeInTheDocument();
    expect(skillCard.querySelector('.description-text')).toBeInTheDocument();
    expect(skillCard.querySelector('.card-tags')).toBeInTheDocument();

    // Assert Card Status Badge and Compact Actions
    expect(skillCard.querySelector('.mcp-tool-status.is-enabled')).toBeInTheDocument();
    const cardActions = skillCard.querySelector('.card-actions-redesign');
    expect(cardActions).toBeInTheDocument();
    expect(within(cardActions).getByRole('button', { name: '编辑详情' })).toBeInTheDocument();
    expect(within(cardActions).getByRole('button', { name: '删除' })).toBeInTheDocument();

    // 3. DataSource Tab
    fireEvent.click(screen.getByRole('button', { name: /数据源|Data Sources/ }));

    const datasourceContainer = container.querySelector('.datasource-container');
    expect(datasourceContainer).toBeInTheDocument();

    // Assert Left-aligned Page Header with Refresh Button
    const dsHeader = datasourceContainer.querySelector('.plugins-square-header');
    expect(dsHeader).toBeInTheDocument();
    expect(within(dsHeader).getByRole('heading', { name: '内部数据源', level: 1 })).toBeInTheDocument();
    expect(dsHeader.querySelector('.plugins-square-subtitle')).not.toBeInTheDocument();
    expect(within(dsHeader).getByRole('button', { name: '刷新' })).toBeInTheDocument();

    // Assert Import local datasource card
    const importCard = screen.getByTestId('datasource-import-zone');
    expect(importCard).toHaveClass('mcp-tool-card', 'add-new-card');
    expect(within(importCard).getByRole('heading', { name: '导入本地数据源', level: 2 })).toBeInTheDocument();
    expect(within(importCard).getByRole('button', { name: '导入' })).toBeInTheDocument();

    // Assert Compact Search bar
    expect(screen.getByPlaceholderText('搜索内部数据源')).toBeInTheDocument();

    // Assert Datasource Document Card (using mocked data)
    await screen.findByRole('heading', { name: 'source.txt' });
    const dsCard = datasourceContainer.querySelector('.datasource-card');
    expect(dsCard).toBeInTheDocument();
    expect(within(dsCard).getByRole('heading', { name: 'source.txt', level: 3 })).toBeInTheDocument();
    expect(dsCard.querySelector('.datasource-card-path')).toBeInTheDocument();
    expect(dsCard.querySelector('.datasource-card-meta')).toBeInTheDocument();

    // Assert Datasource Card Status Badge and Actions
    expect(dsCard.querySelector('.mcp-tool-status.is-enabled')).toBeInTheDocument();
    const dsActions = dsCard.querySelector('.datasource-card-actions');
    expect(dsActions).toBeInTheDocument();
    expect(within(dsActions).getByRole('button', { name: '查看 source.txt' })).toBeInTheDocument();
    expect(within(dsActions).getByRole('button', { name: '编辑 source.txt' })).toBeInTheDocument();
    expect(within(dsActions).getByRole('button', { name: '删除 source.txt' })).toBeInTheDocument();
  });

  it('renders template 1 empty state card when datasource list is empty', async () => {
    backend.listDatasourceDocuments.mockResolvedValue({ documents: [] });
    renderSkillsPage();
    fireEvent.click(screen.getByRole('button', { name: /数据源|Data Sources/ }));
    const emptyState = await screen.findByTestId('datasource-empty-state');
    expect(emptyState).toHaveClass('empty-state', 'datasource-empty-card');
    expect(within(emptyState).getByRole('heading', { name: '暂无数据源' })).toBeInTheDocument();
  });
});
