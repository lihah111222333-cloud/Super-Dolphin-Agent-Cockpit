import { logDebug } from '../services/log.js';

export const SidebarNav = {
  name: 'SidebarNav',
  props: {
    page: { type: String, required: true },
    items: { type: Array, required: true },
  },
  emits: ['change'],
  setup(props, { emit }) {
    function onChange(target) {
      logDebug('ui', 'sidebar.change', {
        from: props.page,
        to: target,
      });
      emit('change', target);
    }

    return {
      onChange,
    };
  },
  template: `
    <nav id="sidebar" data-testid="sidebar-nav">
      <button
        v-for="item in items"
        :key="item.key"
        class="sidebar-btn"
        :class="{ active: item.key === page }"
        :data-testid="'nav-' + item.key"
        @click="onChange(item.key)"
      >
        <span class="sb-icon" v-html="item.icon"></span>
        <span class="sb-label">{{ item.label }}</span>
      </button>
      <div class="sidebar-spacer"></div>
    </nav>
  `,
};
