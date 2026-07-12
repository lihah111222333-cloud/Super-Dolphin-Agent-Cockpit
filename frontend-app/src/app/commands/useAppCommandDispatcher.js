import { useEffect } from 'react';

import { matchesShortcut } from '../../shared/keyboard/shortcutModel.js';

function dispatchCommandShortcut(event, runtime) {
  for (const command of runtime.commands) {
    if (!matchesShortcut(event, command.shortcut, {
      editablePolicy: command.editablePolicy === 'allow' ? 'allow' : 'deny',
      repeatable: command.repeatable === true,
    })) continue;
    const result = runtime.execute(command.id);
    if (result.executed) event.preventDefault();
    return;
  }
}

export function useAppCommandDispatcher({ eventTarget = window, runtime }) {
  useEffect(() => {
    const onKeyDown = (event) => dispatchCommandShortcut(event, runtime);
    eventTarget.addEventListener('keydown', onKeyDown);
    return () => eventTarget.removeEventListener('keydown', onKeyDown);
  }, [eventTarget, runtime]);
}
