import React from 'react';
import { APP_COPY } from '../../../shared/i18n/appI18n.js';

const ComposerTextarea = React.forwardRef(function ComposerTextarea({
  copy = APP_COPY.zh.chat,
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
      aria-label={copy.inputLabel}
      rows={3}
      value={draft}
      onChange={onChange}
      onPaste={onPaste}
      onCompositionStart={onCompositionStart}
      onCompositionEnd={onCompositionEnd}
      onKeyDown={onKeyDown}
      placeholder={copy.inputPlaceholder}
    />
  );
});

export { ComposerTextarea };
