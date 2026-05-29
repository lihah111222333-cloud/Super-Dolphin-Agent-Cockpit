import React from 'react';
import { useVueSetup, val } from '../../utils/vue-compat.js';
import { DagFinalOutputPanel as VueComp } from './DagFinalOutputPanel.js';

export function DagFinalOutputPanel(props) {
  const emit = (event, ...args) => {
    const propName = 'on' + event.split('-').map(s => s[0].toUpperCase() + s.slice(1)).join('');
    props[propName]?.(...args);
  };
  const vm = useVueSetup(VueComp.setup, props, emit);

  const runsErrorText = val(vm.runsErrorText);
  const outputPath = val(vm.outputPath);
  const outputKind = val(vm.outputKind);
  const reading = val(vm.reading);
  const fileErrorText = val(vm.fileErrorText);
  const fileContent = val(vm.fileContent);
  const previewText = val(vm.previewText);

  return (
    <section className="dag-detail-section dag-final-output-panel" data-testid="dag-final-output-panel">
      <div className="dag-section-title">最终结果</div>
      {runsErrorText ? (
        <div className="dag-console-error-inline" data-testid="dag-runs-error">{runsErrorText}</div>
      ) : (
        <div>
          {outputPath ? (
            <div className="dag-final-file">
              <div className="dag-final-kind">{outputKind}</div>
              <code>{outputPath}</code>
              <div className="dag-final-actions">
                <button
                  type="button"
                  className="btn btn-ghost"
                  data-method="ui/memory/shared-file/get"
                  data-testid="dag-final-output-open"
                  disabled={reading}
                  onClick={vm.openFile}
                >
                  打开
                </button>
                <button
                  type="button"
                  className="btn btn-ghost"
                  data-method="ui/memory/shared-file/get"
                  data-testid="dag-final-output-read"
                  disabled={reading}
                  onClick={vm.readFile}
                >
                  读取
                </button>
              </div>
              {fileErrorText && (
                <div className="dag-console-error-inline" data-testid="dag-final-output-error">
                  {fileErrorText}
                </div>
              )}
              {fileContent && (
                <pre className="dag-final-preview" data-testid="dag-final-output-content">
                  {fileContent}
                </pre>
              )}
            </div>
          ) : props.finalOutput ? (
            <pre className="dag-final-preview" data-testid="dag-final-output-preview">
              {previewText}
            </pre>
          ) : (
            <div className="dag-console-muted" data-testid="dag-final-output-empty">
              当前运行尚未标记最终结果。
            </div>
          )}
        </div>
      )}
    </section>
  );
}
