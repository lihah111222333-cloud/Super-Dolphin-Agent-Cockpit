import React from 'react';
import { useVueSetup, val } from '../../utils/vue-compat.js';
import { BuiltinToolsSettings as VueComp } from './BuiltinToolsSettings.ts';

export function BuiltinToolsSettings(props) {
  const emit = (event, ...args) => {
    const propName = 'on' + event.split('-').map(s => s[0].toUpperCase() + s.slice(1)).join('');
    props[propName]?.(...args);
  };
  const vm = useVueSetup(VueComp.setup, props, emit);

  const tools = val(vm.tools) || [];
  const groups = val(vm.groups) || [];
  const filteredCount = val(vm.filteredCount);
  const totalToolCount = val(vm.totalToolCount);
  const loading = val(vm.loading);
  const savingIds = vm.savingIds || {};
  const notice = vm.notice || {};

  return (
    <>
      <div className="section-header">模型内置能力</div>
      <div className="data-card-vue" data-testid="settings-builtin-tools-card">
        <div className="data-row-vue">
          <strong>内置能力开关</strong>
          <span data-testid="settings-builtin-tools-summary">
            {loading ? '加载中...' : `已管控 ${filteredCount} / ${totalToolCount}`}
          </span>
        </div>
        <div className="settings-prompt-desc">
          默认管控与本项目文件、命令、编排、计划、权限、插件管理重复，或会绕过项目治理的能力。
        </div>

        {tools.length === 0 && !loading ? (
          <div className="settings-log-empty" data-testid="settings-builtin-tools-empty">
            暂无可配置的内置工具
          </div>
        ) : (
          <div className="settings-builtin-tool-groups" data-testid="settings-builtin-tools-groups">
            {groups.map((group) => (
              <section
                key={group.key}
                className="settings-builtin-tool-group"
                data-testid={`settings-builtin-tool-group-${group.key}`}
              >
                <button
                  type="button"
                  className="settings-builtin-tool-group-head"
                  data-testid={`settings-builtin-tool-group-head-${group.key}`}
                  aria-expanded={vm.isGroupExpanded(group.key) ? 'true' : 'false'}
                  onClick={() => vm.toggleGroupExpanded(group.key)}
                >
                  <span className={`settings-builtin-tool-group-chevron ${vm.isGroupExpanded(group.key) ? 'is-open' : ''}`}>▸</span>
                  <span className="settings-builtin-tool-group-name">{group.label}</span>
                  <span className="settings-builtin-tool-group-summary">
                    {vm.groupSummary(group)}
                  </span>
                </button>
                {vm.isGroupExpanded(group.key) && (
                  <div className="settings-builtin-tool-group-body">
                    {group.note && (
                      <p className="settings-builtin-tool-group-note" data-testid={`settings-builtin-tool-group-note-${group.key}`}>
                        {group.note}
                      </p>
                    )}
                    {group.tools.map((tool) => (
                      <label
                        key={tool.id}
                        className={`settings-prompt-toggle ${(!tool.enabled || tool.replacedBy) ? 'is-disabled-tool' : ''}`}
                        data-testid={`settings-builtin-tool-${tool.id}`}
                      >
                        <div className="settings-prompt-toggle-copy">
                          <span className="settings-prompt-toggle-title">{tool.label}</span>
                          <span className="settings-prompt-toggle-desc">
                            {vm.toolMetaText(tool)}
                          </span>
                        </div>
                        <input
                          type="checkbox"
                          className="settings-prompt-toggle-input"
                          data-testid={`settings-builtin-tool-input-${tool.id}`}
                          checked={!tool.enabled || !!tool.replacedBy}
                          disabled={!!tool.replacedBy || savingIds[tool.id]}
                          onChange={() => vm.toggleBuiltinTool(tool)}
                        />
                      </label>
                    ))}
                  </div>
                )}
              </section>
            ))}
          </div>
        )}
        {notice.message && (
          <div className={`settings-prompt-notice is-${notice.level}`} data-testid="settings-builtin-tools-notice">
            {notice.message}
          </div>
        )}
      </div>
    </>
  );
}
