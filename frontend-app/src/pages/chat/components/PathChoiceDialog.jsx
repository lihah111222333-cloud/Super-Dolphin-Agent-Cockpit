import React from 'react';
import { X } from 'lucide-react';
import { FocusTrapDialog } from '../../../shared/ui/FocusTrapDialog.jsx';

function PathChoiceDialog({ choice, onClose, onSelect }) {
  const options = Array.isArray(choice?.options) ? choice.options : [];
  return (
    <FocusTrapDialog ariaLabel="选择文件路径" className="modal-box path-choice-modal" onClose={onClose}>
      <header>
        <div>
          <h2>选择文件路径</h2>
          <p>{choice?.file?.filename || '请选择要打开的文件'}</p>
        </div>
        <button type="button" aria-label="关闭路径选择" title="关闭路径选择" onClick={onClose}>
          <X size={15} aria-hidden="true" />
        </button>
      </header>
      <div className="path-choice-options">
        {options.length > 0 ? options.map((path) => (
          <button className="path-choice-option" key={path} type="button" onClick={() => onSelect(path)}>
            {path}
          </button>
        )) : <p>没有可选路径</p>}
      </div>
      {choice?.truncated ? <p className="path-choice-truncated">结果已截断，仅显示部分结果</p> : null}
      <footer>
        <button type="button" onClick={onClose}>取消</button>
      </footer>
    </FocusTrapDialog>
  );
}

export { PathChoiceDialog };
