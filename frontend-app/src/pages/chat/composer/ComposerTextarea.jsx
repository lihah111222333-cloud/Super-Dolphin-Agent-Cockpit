import React from 'react';
import { APP_COPY } from '../../../shared/i18n/appI18n.js';

const ComposerTextarea = React.forwardRef(function ComposerTextarea({
  ariaActiveDescendant,
  ariaControls,
  ariaExpanded,
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
      aria-activedescendant={ariaActiveDescendant}
      aria-autocomplete="list"
      aria-controls={ariaControls}
      aria-expanded={ariaExpanded}
      aria-haspopup="listbox"
      aria-label={copy.inputLabel}
      role="combobox"
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
