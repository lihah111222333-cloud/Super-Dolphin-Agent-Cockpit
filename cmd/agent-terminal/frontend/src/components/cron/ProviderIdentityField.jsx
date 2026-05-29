import React from 'react';
import { useVueSetup, val } from '../../utils/vue-compat.js';
import { ProviderIdentityField as VueComp } from './ProviderIdentityField.js';

export function ProviderIdentityField(props) {
  const emit = (event, ...args) => {
    if (event === 'update:modelValue') {
      props.onChange?.(...args);
    }
  };
  const vm = useVueSetup(VueComp.setup, props, emit);

  const modelValue = props.modelValue || {
    provider: 'codex',
    model: '',
    config: { codexHome: '', codexInstanceKey: '', codexModelProvider: '' },
  };

  return (
    <div className="provider-identity-field" data-testid="provider-identity-field">
      <div className="form-row">
        <label>Provider *</label>
        <select
          value={modelValue.provider}
          data-testid="provider-select"
          onChange={(e) => vm.update({ provider: e.target.value })}
        >
          <option value="codex">codex</option>
          <option value="claude" disabled>claude（v1 暂不支持）</option>
        </select>
      </div>
      <div className="form-row">
        <label>Model</label>
        <input
          type="text"
          value={modelValue.model || ''}
          placeholder="留空使用 provider 默认"
          data-testid="provider-model-input"
          onChange={(e) => vm.update({ model: e.target.value })}
        />
      </div>

      {modelValue.provider === 'codex' && (
        <fieldset className="codex-identity" data-testid="codex-identity-fieldset">
          <legend>Codex 实例身份（三项必填）</legend>
          <div className="form-row">
            <label>codexHome *</label>
            <input
              type="text"
              value={(modelValue.config && modelValue.config.codexHome) || ''}
              placeholder="例如 /Users/demo/.codex-providers/glm"
              data-testid="codex-home-input"
              onChange={(e) => vm.updateConfig({ codexHome: e.target.value })}
            />
          </div>
          <div className="form-row">
            <label>codexInstanceKey *</label>
            <input
              type="text"
              value={(modelValue.config && modelValue.config.codexInstanceKey) || ''}
              placeholder="例如 glm"
              data-testid="codex-instance-key-input"
              onChange={(e) => vm.updateConfig({ codexInstanceKey: e.target.value })}
            />
          </div>
          <div className="form-row">
            <label>codexModelProvider *</label>
            <input
              type="text"
              value={(modelValue.config && modelValue.config.codexModelProvider) || ''}
              placeholder="例如 glm-compat"
              data-testid="codex-model-provider-input"
              onChange={(e) => vm.updateConfig({ codexModelProvider: e.target.value })}
            />
          </div>
        </fieldset>
      )}
    </div>
  );
}
