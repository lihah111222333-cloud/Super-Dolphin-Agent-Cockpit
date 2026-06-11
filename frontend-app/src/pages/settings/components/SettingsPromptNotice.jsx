import React from 'react';

function SettingsPromptNotice({ className = '', notice, testId = '' }) {
  return (
    <div
      className={'settings-prompt-notice ' + className + ' is-' + notice.level}
      data-testid={testId || undefined}
      role={notice.level === 'error' ? 'alert' : 'status'}
    >
      {notice.message}
    </div>
  );
}

export { SettingsPromptNotice };
