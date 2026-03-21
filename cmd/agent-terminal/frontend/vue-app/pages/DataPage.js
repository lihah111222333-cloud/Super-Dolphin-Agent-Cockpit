import { onBeforeUnmount, onMounted, watch } from '../../lib/vue.esm-browser.prod.js';
import { logDebug, logInfo } from '../services/log.js';

export const DataPage = {
  name: 'DataPage',
  props: {
    pageId: { type: String, required: true },
    title: { type: String, required: true },
    icon: { type: String, default: '·' },
    items: { type: Array, default: () => [] },
    emptyText: { type: String, default: '暂无数据' },
    fields: { type: Array, default: () => [] },
  },
  setup(props) {
    watch(
      () => props.items.length,
      (next, prev) => {
        if (next === prev) return;
        logDebug('page', 'data.items.changed', {
          page: props.pageId,
          count: next,
        });
      },
      { immediate: true },
    );

    onMounted(() => {
      logInfo('page', 'data.mounted', {
        page: props.pageId,
      });
    });
    onBeforeUnmount(() => {
      logInfo('page', 'data.unmounted', {
        page: props.pageId,
      });
    });
    return {};
  },
  template: `
    <section :id="'page-' + pageId" class="page active" :data-testid="'data-page-' + pageId">
      <div class="panel-header" :data-testid="'data-page-header-' + pageId">
        <div class="ph-bar"></div>
        <div class="ph-text">
          <h2>{{ title }}</h2>
        </div>
      </div>
      <div class="panel-body" :data-testid="'data-page-body-' + pageId">
        <div v-if="items.length === 0" class="empty-state" :data-testid="'data-page-empty-' + pageId">
          <div class="es-icon">{{ icon }}</div>
          <h3>{{ emptyText }}</h3>
        </div>
        <div v-else class="data-list-vue" :data-testid="'data-page-list-' + pageId">
          <article
            v-for="(item, idx) in items"
            :key="item.id || item[fields[0]?.key] || idx"
            class="data-card-vue"
            :data-testid="'data-page-card-' + pageId + '-' + idx"
          >
            <div v-for="field in fields" :key="field.key" class="data-row-vue">
              <strong>{{ field.label }}</strong>
              <span>{{ item[field.key] ?? '-' }}</span>
            </div>
          </article>
        </div>
      </div>
    </section>
  `,
};
