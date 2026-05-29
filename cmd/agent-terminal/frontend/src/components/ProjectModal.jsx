import React from 'react';
import { logDebug } from '../services/log.js';
import { projectStoreVanilla } from '../stores/projects.js';

export function ProjectModal({ store }) {
  const { showModal, modalPath, browsing } = store.state;
  const canConfirm = Boolean((modalPath || '').trim());

  if (!showModal) return null;

  const closeByMask = () => {
    logDebug('ui', 'projectModal.mask.close', {});
    store.closeModal();
  };

  const onConfirm = () => {
    logDebug('ui', 'projectModal.confirm.click', {
      path: modalPath || '',
    });
    store.confirmModal();
  };

  const onBrowse = () => {
    logDebug('ui', 'projectModal.browse.click', {});
    store.browseDirectory();
  };

  const handleKeyDown = (e) => {
    if (e.key === 'Enter' && canConfirm) {
      onConfirm();
    } else if (e.key === 'Escape') {
      closeByMask();
    }
  };

  return (
    <div 
      className="modal-overlay" 
      onClick={(e) => {
        if (e.target === e.currentTarget) closeByMask();
      }}
    >
      <div className="modal-box">
        <div className="modal-title">添加项目</div>
        <div className="modal-input-row">
          <input
            className="modal-input modal-input-flex"
            type="text"
            placeholder="/Users/you/projects/my-app"
            spellcheck="false"
            autoComplete="off"
            value={modalPath}
            onChange={(e) => projectStoreVanilla.setState({ modalPath: e.target.value })}
            onKeyDown={handleKeyDown}
            autoFocus
          />
          <button 
            className="btn btn-secondary modal-browse-btn" 
            onClick={onBrowse} 
            disabled={browsing}
          >
            {browsing ? '打开中...' : '浏览...'}
          </button>
        </div>
        <div className="modal-btns">
          <button className="btn btn-ghost" onClick={closeByMask}>取消</button>
          <button className="btn btn-primary" onClick={onConfirm} disabled={!canConfirm}>确定</button>
        </div>
      </div>
    </div>
  );
}
