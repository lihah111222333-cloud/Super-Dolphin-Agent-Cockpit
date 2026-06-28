// @ts-nocheck
// 顶部警示横幅：当前活跃会话上下文使用率跨过阈值（warn/danger/critical）时展示。
// 状态由父级 (UnifiedChatPage) 传入；本组件本身不持有状态。

export const ContextUsageBanner = {
  name: 'ContextUsageBanner',
  props: {
    level: { type: String, default: 'normal' },
    usedPercent: { type: Number, default: 0 },
    usedTokens: { type: Number, default: 0 },
    contextWindow: { type: Number, default: 0 },
    canCompact: { type: Boolean, default: true },
    compacting: { type: Boolean, default: false },
  },
  emits: ['compact', 'fork'],
  setup(props, { emit }) {
    function levelLabel() {
      if (props.level === 'critical') return '严重';
      if (props.level === 'danger') return '紧张';
      if (props.level === 'warn') return '提醒';
      return '';
    }
    function showTokenSection() { return props.level && props.level !== 'normal'; }
    function visible() { return showTokenSection(); }
    function onCompact() {
      if (props.compacting || !props.canCompact) return;
      emit('compact');
    }
    function onFork() { emit('fork'); }
    return {
      levelLabel, showTokenSection, visible, onCompact, onFork,
    };
  },
  template: `
    <div
      v-if="visible()"
      class="context-usage-banner"
      :class="'is-token-' + level"
      role="alert"
      data-testid="context-usage-banner"
    >
      <template v-if="showTokenSection()">
        <span class="context-usage-banner-icon" aria-hidden="true">⚠</span>
        <span class="context-usage-banner-msg">
          上下文使用率
          <strong>{{ Math.round(Number(usedPercent) || 0) }}%</strong>
          <span v-if="contextWindow > 0" style="opacity: 0.75; margin-left: 4px;">
            ({{ usedTokens }} / {{ contextWindow }} tokens)
          </span>
          — {{ levelLabel() }}，建议压缩上下文或新建继承会话以避免触发自动截断。
        </span>
        <button
          type="button"
          class="btn btn-ghost btn-xs context-usage-banner-action"
          :disabled="!canCompact || compacting"
          :title="!canCompact ? '当前 agent 不支持上下文压缩' : (compacting ? '正在压缩…' : '触发上下文压缩')"
          @click="onCompact"
        >{{ compacting ? '压缩中…' : '压缩上下文' }}</button>
        <button
          type="button"
          class="btn btn-primary btn-xs context-usage-banner-action"
          title="以当前会话为背景新建一个继承对话"
          @click="onFork"
        >新建继承会话</button>
      </template>
    </div>
  `,
};
