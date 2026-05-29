import React, { useRef, useEffect } from 'react';
import { ref, computed, watch, nextTick } from '../../lib/vue.esm-browser.prod.js';

function normalizeOptions(options) {
  if (!Array.isArray(options)) return [];
  return options
    .map((item) => (item || '').toString().trim())
    .filter(Boolean);
}

export function PathChoiceModal({
  show = false,
  options = [],
  title = '选择文件路径',
  truncated = false,
  onConfirm = null,
  onCancel = null,
}) {
  const modalRef = useRef(null);
  const normalizedOptions = normalizeOptions(options);

  useEffect(() => {
    if (show && modalRef.current) {
      modalRef.current.focus();
    }
  }, [show]);

  if (!show) return null;

  const confirmPath = (path) => {
    const selectedPath = (path || '').toString().trim();
    if (!selectedPath) return;
    if (typeof onConfirm === 'function') onConfirm(selectedPath);
  };

  const cancelPathChoice = () => {
    if (typeof onCancel === 'function') onCancel();
  };

  const handleKeyDown = (e) => {
    if (e.key === 'Escape') {
      e.stopPropagation();
      e.preventDefault();
      cancelPathChoice();
    }
  };

  return (
    <div
      ref={modalRef}
      className="modal-overlay"
      role="dialog"
      aria-modal="true"
      aria-label={title || '选择文件路径'}
      tabIndex={-1}
      data-testid="path-choice-modal"
      onClick={(e) => {
        if (e.target === e.currentTarget) cancelPathChoice();
      }}
      onKeyDown={handleKeyDown}
      style={{ outline: 'none' }}
    >
      <div className="modal-box" style={{ minWidth: '420px', maxWidth: '760px' }}>
        <div className="modal-title">{title || '选择文件路径'}</div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: '8px', maxHeight: '320px', overflow: 'auto' }}>
          {!normalizedOptions.length ? (
            <div
              data-testid="path-choice-empty"
              style={{ color: 'var(--text-muted)', fontSize: '13px', padding: '12px 0', textAlign: 'center' }}
            >
              没有可选路径
            </div>
          ) : (
            normalizedOptions.map((option, index) => (
              <button
                key={`${option}:${index}`}
                className="btn btn-ghost"
                type="button"
                title={option}
                data-testid={`path-choice-option-${index}`}
                onClick={() => confirmPath(option)}
                style={{ display: 'flex', justifyContent: 'flex-start', width: '100%', cursor: 'pointer', overflow: 'hidden' }}
              >
                <span style={{ fontFamily: 'var(--font-mono)', textAlign: 'left', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', width: '100%' }}>
                  {option}
                </span>
              </button>
            ))
          )}
        </div>
        {truncated && (
          <div
            data-testid="path-choice-truncated"
            style={{ marginTop: '12px', color: 'var(--text-muted)', fontSize: '12px' }}
          >
            结果已截断，仅显示部分结果
          </div>
        )}
        <div className="modal-btns">
          <button 
            className="btn btn-ghost" 
            type="button" 
            data-testid="path-choice-cancel" 
            onClick={cancelPathChoice}
          >
            取消
          </button>
        </div>
      </div>
    </div>
  );
}

// Vue setup helper function mounted for compatibility with unit tests
PathChoiceModal.setup = function (props, setupCtx = {}) {
  const modalRef = ref(null);
  
  const normalizedOptions = computed(() => {
    if (!props.options || !Array.isArray(props.options)) return [];
    return props.options
      .map((item) => (item || '').toString().trim())
      .filter(Boolean);
  });

  const confirmPath = (path) => {
    const selectedPath = (path || '').toString().trim();
    if (!selectedPath) return;
    if (typeof props.onConfirm === 'function') {
      props.onConfirm(selectedPath);
    }
  };

  const cancelPathChoice = () => {
    if (typeof props.onCancel === 'function') {
      props.onCancel();
    }
  };

  const onEscapeKey = (e) => {
    if (e) {
      if (typeof e.preventDefault === 'function') e.preventDefault();
      if (typeof e.stopPropagation === 'function') e.stopPropagation();
    }
    cancelPathChoice();
  };

  watch(
    () => props.show,
    (nextShow) => {
      if (nextShow) {
        nextTick(() => {
          if (modalRef.value && typeof modalRef.value.focus === 'function') {
            modalRef.value.focus();
          }
        });
      }
    },
    { immediate: true }
  );

  return {
    normalizedOptions,
    confirmPath,
    cancelPathChoice,
    modalRef,
    onEscapeKey,
  };
};

PathChoiceModal.template = `
  <div data-testid="path-choice-modal" @keydown.escape="onEscapeKey">
    <button v-for="(option, index) in normalizedOptions" :data-testid="'path-choice-option-' + index" @click="confirmPath(option)">
      {{ option }}
    </button>
    <div v-if="!normalizedOptions.length" data-testid="path-choice-empty">没有可选路径</div>
    <div v-if="truncated">结果已截断，仅显示部分结果</div>
  </div>
`;

export default PathChoiceModal;
