import React from 'react';
import { useVueSetup } from '../utils/vue-compat.js';
import { CommandsPage as VueComp } from './CommandsPage.js';

export function CommandsPage(props) {
  const emit = (event, ...args) => {
    const propName = 'on' + event.split('-').map(s => s[0].toUpperCase() + s.slice(1)).join('');
    props[propName]?.(...args);
  };
  const vm = useVueSetup(VueComp.setup, props, emit);

  const commandCards = props.commandCards || [];
  const commandFields = props.commandFields || [];

  return (
    <section id="page-commands" className="page active" data-testid="commands-page">
      <div className="panel-header">
        <div className="ph-bar"></div>
        <div className="ph-text"><h2>命令卡</h2></div>
      </div>
      <div className="panel-body" data-testid="commands-panel">
        {commandCards.length === 0 ? (
          <div className="empty-state" data-testid="commands-empty-state">
            <div className="es-icon">C</div>
            <h3>暂无命令卡</h3>
          </div>
        ) : (
          <div className="data-list-vue" data-testid="commands-list">
            {commandCards.map((item, idx) => (
              <article
                key={item.card_key || ('cmd-' + idx)}
                className="data-card-vue"
                data-testid={'command-card-' + idx}
              >
                {commandFields.map((field) => (
                  <div key={field.key} className="data-row-vue">
                    <strong>{field.label}</strong>
                    <span>{item[field.key] ?? '-'}</span>
                  </div>
                ))}
                <div className="data-actions-vue">
                  <button
                    className="btn btn-ghost btn-xs"
                    data-testid={'command-run-button-' + idx}
                    onClick={() => vm.onRunCommand(item)}
                  >
                    发送到当前会话
                  </button>
                </div>
              </article>
            ))}
          </div>
        )}
      </div>
    </section>
  );
}
