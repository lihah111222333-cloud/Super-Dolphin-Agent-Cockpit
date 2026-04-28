// ProviderIdentityField: provider 选择 + codex identity 三字段。
// v1 仅 codex；claude 选项 disabled，配文案说明。三字段在 provider=codex
// 时强制必填，缺失会被后端 ErrInvalidConfig 拒绝（contract.go:38）。
export const ProviderIdentityField = {
  name: 'ProviderIdentityField',
  props: {
    modelValue: {
      type: Object,
      default: () => ({
        provider: 'codex',
        model: '',
        config: { codexHome: '', codexInstanceKey: '', codexModelProvider: '' },
      }),
    },
  },
  emits: ['update:modelValue'],
  setup(props, { emit }) {
    function update(patch) {
      emit('update:modelValue', { ...props.modelValue, ...patch });
    }
    function updateConfig(patch) {
      const baseConfig = props.modelValue.config && typeof props.modelValue.config === 'object'
        ? props.modelValue.config
        : {};
      update({ config: { ...baseConfig, ...patch } });
    }
    return { update, updateConfig };
  },
  template: `
    <div class="provider-identity-field" data-testid="provider-identity-field">
      <div class="form-row">
        <label>Provider *</label>
        <select
          :value="modelValue.provider"
          data-testid="provider-select"
          @change="update({ provider: $event.target.value })"
        >
          <option value="codex">codex</option>
          <option value="claude" disabled>claude（v1 暂不支持）</option>
        </select>
      </div>
      <div class="form-row">
        <label>Model</label>
        <input
          type="text"
          :value="modelValue.model || ''"
          placeholder="留空使用 provider 默认"
          data-testid="provider-model-input"
          @input="update({ model: $event.target.value })"
        />
      </div>

      <fieldset
        v-if="modelValue.provider === 'codex'"
        class="codex-identity"
        data-testid="codex-identity-fieldset"
      >
        <legend>Codex 实例身份（三项必填）</legend>
        <div class="form-row">
          <label>codexHome *</label>
          <input
            type="text"
            :value="(modelValue.config && modelValue.config.codexHome) || ''"
            placeholder="例如 /Users/demo/.codex-providers/glm"
            data-testid="codex-home-input"
            @input="updateConfig({ codexHome: $event.target.value })"
          />
        </div>
        <div class="form-row">
          <label>codexInstanceKey *</label>
          <input
            type="text"
            :value="(modelValue.config && modelValue.config.codexInstanceKey) || ''"
            placeholder="例如 glm"
            data-testid="codex-instance-key-input"
            @input="updateConfig({ codexInstanceKey: $event.target.value })"
          />
        </div>
        <div class="form-row">
          <label>codexModelProvider *</label>
          <input
            type="text"
            :value="(modelValue.config && modelValue.config.codexModelProvider) || ''"
            placeholder="例如 glm-compat"
            data-testid="codex-model-provider-input"
            @input="updateConfig({ codexModelProvider: $event.target.value })"
          />
        </div>
      </fieldset>
    </div>
  `,
};
