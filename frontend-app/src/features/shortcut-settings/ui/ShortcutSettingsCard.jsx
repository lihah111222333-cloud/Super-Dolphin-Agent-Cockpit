import React from 'react';

import './ShortcutSettingsCard.css';

function capturedShortcut(event) {
  if (event.defaultPrevented || event.isComposing || event.repeat) return null;
  const keyCode = Number(event.keyCode || event.which || 0);
  if (keyCode === 229 || event.key === 'Process' || event.key === 'Unidentified') return null;
  if (!event.metaKey && !event.ctrlKey && !event.altKey) return null;
  if (['Meta', 'Control', 'Alt', 'Shift'].includes(event.key)) return null;
  return {
    key: event.key.toLocaleLowerCase(),
    meta: Boolean(event.metaKey),
    ctrl: Boolean(event.ctrlKey),
    alt: Boolean(event.altKey),
    shift: Boolean(event.shiftKey),
  };
}

function ShortcutSettingRow({ command, controller, copy }) {
  return (
    <li className="shortcut-setting" data-testid={`shortcut-setting-${command.id}`}>
      <span className="shortcut-setting__copy">
        <strong>{command.label}</strong>
        <small>{command.help}</small>
      </span>
      <span className="shortcut-setting__keys">
        <small>{command.defaultDisplay}</small>
        <button
          type="button"
          aria-label={`${copy.edit} ${command.label}`}
          onKeyDown={(event) => {
            const shortcut = capturedShortcut(event);
            if (!shortcut) return;
            event.preventDefault();
            controller.setDraftBinding(command.id, shortcut);
          }}
        >
          {command.currentDisplay}
        </button>
      </span>
    </li>
  );
}

export function ShortcutSettingsCard({ controller, copy }) {
  const busy = controller.status === 'loading' || controller.status === 'saving' || controller.status === 'unavailable';
  return (
    <section className="settings-card shortcut-settings-card" data-testid="shortcut-settings-card">
      <header>
        <div>
          <h2>{copy.title}</h2>
          <p>{copy.description}</p>
        </div>
      </header>
      {controller.status === 'loading' ? <p>{copy.loading}</p> : null}
      {controller.error ? <p role="alert" className="shortcut-settings-card__error">{controller.error}</p> : null}
      <ul className="shortcut-settings-card__list">
        {controller.commands.map((command) => (
          <ShortcutSettingRow command={command} controller={controller} copy={copy} key={command.id} />
        ))}
      </ul>
      <footer>
        <button type="button" className="btn btn-secondary" disabled={busy} onClick={() => { void controller.reset(); }}>
          {copy.reset}
        </button>
        <button type="button" className="btn btn-primary" disabled={busy} onClick={() => { void controller.save(); }}>
          {copy.save}
        </button>
      </footer>
    </section>
  );
}
