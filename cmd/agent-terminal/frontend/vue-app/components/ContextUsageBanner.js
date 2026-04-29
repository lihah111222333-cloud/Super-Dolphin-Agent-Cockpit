// @ts-nocheck
// 顶部警示横幅：当前活跃会话上下文使用率跨过阈值（warn/danger/critical）时展示。
// Phase 1.4d：自动续接失败时也会显示重试入口（即使 token level=normal，例如 status_error 触发的失败）。
// 状态由父级 (UnifiedChatPage) 传入；本组件本身不持有状态。

const AUTO_CONTINUE_REASON_LABEL = Object.freeze({
  compact_then_continue_failed: '上下文压缩与自动续接均失败',
  continue_failed: '自动续接失败',
  recover_then_continue_failed: '进程恢复与自动续接均失败',
  gated_thread_already_continued: '此 thread 已自动续接过 1 次',
  gated_global_fuse_blown: '系统级自动续接保险丝已触发',
});

export const ContextUsageBanner = {
  name: 'ContextUsageBanner',
  props: {
    level: { type: String, default: 'normal' },
    usedPercent: { type: Number, default: 0 },
    usedTokens: { type: Number, default: 0 },
    contextWindow: { type: Number, default: 0 },
    canCompact: { type: Boolean, default: true },
    compacting: { type: Boolean, default: false },
    // Phase 1.4d：自动续接失败信息。null = 无失败。
    // 形状：{ kind, last_action, reason, error_message, ts }
    failedInfo: { type: Object, default: null },
    retrying: { type: Boolean, default: false },
    retryError: { type: String, default: '' }, // R2 fix：一键重试失败反馈
    stuckInfo: { type: Object, default: null },
    pokingStuck: { type: Boolean, default: false },
  },
  emits: ['compact', 'fork', 'retry-auto-continue', 'retry-stuck-thread'],
  setup(props, { emit }) {
    function levelLabel() {
      if (props.level === 'critical') return '严重';
      if (props.level === 'danger') return '紧张';
      if (props.level === 'warn') return '提醒';
      return '';
    }
    function failedReasonLabel() {
      if (!props.failedInfo) return '';
      return AUTO_CONTINUE_REASON_LABEL[props.failedInfo.reason] || '自动续接失败';
    }
    function failedErrorSnippet() {
      const msg = (props.failedInfo && props.failedInfo.error_message) || '';
      return msg.toString().slice(0, 120);
    }
    function showTokenSection() { return props.level && props.level !== 'normal'; }
    function showFailedSection() { return Boolean(props.failedInfo); }
    function showStuckSection() { return Boolean(props.stuckInfo); }
    function visible() { return showTokenSection() || showFailedSection() || showStuckSection(); }
    function stuckDurationLabel() {
      const stuckTs = Number(props.stuckInfo && props.stuckInfo.stuckSinceTs) || 0;
      if (!stuckTs) return '一段时间';
      const sec = Math.max(0, Math.round((Date.now() - stuckTs) / 1000));
      if (sec < 60) return sec + ' 秒';
      const min = Math.round(sec / 60);
      return min + ' 分钟';
    }
    function onCompact() {
      if (props.compacting || !props.canCompact) return;
      emit('compact');
    }
    function onFork() { emit('fork'); }
    function onRetry() {
      if (props.retrying) return;
      emit('retry-auto-continue');
    }
    function onRetryStuck() {
      if (props.pokingStuck) return;
      emit('retry-stuck-thread');
    }
    return {
      levelLabel, failedReasonLabel, failedErrorSnippet,
      showTokenSection, showFailedSection, showStuckSection, stuckDurationLabel, visible,
      onCompact, onFork, onRetry, onRetryStuck,
    };
  },
  template: `
    <div
      v-if="visible()"
      class="context-usage-banner"
      :class="showTokenSection() ? ('is-token-' + level) : 'is-auto-continue-failed'"
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
      <div
        v-if="showFailedSection()"
        class="context-usage-banner-failed"
        data-testid="auto-continue-failed-row"
      >
        <span class="context-usage-banner-icon" aria-hidden="true">⚡</span>
        <span class="context-usage-banner-msg">
          后台自动续接失败：<strong>{{ failedReasonLabel() }}</strong>
          <span v-if="failedErrorSnippet()" style="opacity: 0.75; margin-left: 6px;">— {{ failedErrorSnippet() }}</span>
        </span>
        <button
          type="button"
          class="btn btn-primary btn-xs context-usage-banner-action"
          data-testid="auto-continue-retry-btn"
          :disabled="retrying"
          :title="retrying ? '重试中…' : '手动重试起一个继承对话'"
          @click="onRetry"
        >{{ retrying ? '重试中…' : '一键重试' }}</button>
        <span v-if="retryError" data-testid="auto-continue-retry-error" style="color:var(--color-danger,#c33); margin-left:8px;">{{ retryError }}</span>
      </div>
      <div
        v-if="showStuckSection()"
        class="context-usage-banner-stuck"
        data-testid="thread-stuck-row"
      >
        <span class="context-usage-banner-icon" aria-hidden="true">⏱</span>
        <span class="context-usage-banner-msg">
          agent 似乎卡住 <strong>{{ stuckDurationLabel() }}</strong> — 后端事件流停滞，可手动发送一句“继续”。
        </span>
        <button
          type="button"
          class="btn btn-primary btn-xs context-usage-banner-action"
          data-testid="thread-stuck-poke-btn"
          :disabled="pokingStuck"
          :title="pokingStuck ? '发送中…' : '发送一句继续促 agent 推进'"
          @click="onRetryStuck"
        >{{ pokingStuck ? '发送中…' : '继续' }}</button>
      </div>
    </div>
  `,
};
