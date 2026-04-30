// @ts-nocheck
// Phase 1.5：DAG 一级页面薄包装。
// 顶部挂"自动续接"偏好开关（属自动化任务全局设置；DAG 主题与自动化任务最相关）。
// 下方透传现有 DataPage 渲染 dag 列表（当前 stub，真实数据接通后无需改动本组件）。

import { AutoContinuePrefCard } from '../components/AutoContinuePrefCard.js';
import { DataPage } from './DataPage.js';

export const DagsPage = {
  name: 'DagsPage',
  components: { AutoContinuePrefCard, DataPage },
  props: {
    items: { type: Array, default: () => [] },
    fields: { type: Array, default: () => [] },
    emptyText: { type: String, default: '暂无 DAG' },
  },
  emits: ['select'],
  template: `
    <section id="page-dags" class="page active" data-testid="data-page-dags">
      <AutoContinuePrefCard />
      <DataPage
        page-id="dags"
        title="DAG 管理"
        icon="D"
        :items="items"
        :fields="fields"
        :empty-text="emptyText"
        clickable
        @select="(row) => $emit('select', row)"
      />
    </section>
  `,
};
