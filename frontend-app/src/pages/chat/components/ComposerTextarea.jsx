import React from 'react';

const ComposerTextarea = React.forwardRef(function ComposerTextarea({
  draft,
  onChange,
  onCompositionEnd,
  onCompositionStart,
  onKeyDown,
  onPaste,
}, ref) {
  return (
    <textarea
      ref={ref}
      id="composer-input"
      data-testid="composer-input"
      data-file-drop-target=""
      aria-label="输入给 Agent 的内容"
      rows={3}
      value={draft}
      onChange={onChange}
      onPaste={onPaste}
      onCompositionStart={onCompositionStart}
      onCompositionEnd={onCompositionEnd}
      onKeyDown={onKeyDown}
      placeholder="输入给 Agent 的内容，Enter 发送，Shift+Enter 换行"
    />
  );
});

export { ComposerTextarea };
