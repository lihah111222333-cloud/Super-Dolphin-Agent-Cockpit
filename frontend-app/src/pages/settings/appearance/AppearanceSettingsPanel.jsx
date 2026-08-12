import React, { useId } from 'react';
import './AppearanceSettingsPanel.css';

const THEMES = [
  'system', 'light', 'dark',
];

const ZOOM_LEVELS = [80, 90, 100, 110, 125, 150];

const ACCENTS = [
  { value: 'violet', color: '#7c5cff' },
  { value: 'blue', color: '#2f7df4' },
  { value: 'mint', color: '#21a179' },
  { value: 'amber', color: '#e9812d' },
  { value: 'rose', color: '#df5b78' },
  { value: 'custom', color: '#8d94a6', disabled: true },
];

function AppearanceSettingsPanel({ appearance, copy }) {
  const themeGroupId = useId();
  const zoomGroupId = useId();
  const accentGroupId = useId();
  if (!appearance || typeof appearance !== 'object') {
    throw new Error('AppearanceSettingsPanel requires the global appearance controller');
  }
  if (!copy?.themes || !copy?.accents) {
    throw new Error('AppearanceSettingsPanel requires localized appearance copy');
  }
  const {
    accent,
    reset,
    setAccent,
    setThemeMode,
    setUiScale,
    themeMode: theme,
    uiScale: zoom,
  } = appearance;
  if (
    typeof reset !== 'function'
    || typeof setAccent !== 'function'
    || typeof setThemeMode !== 'function'
    || typeof setUiScale !== 'function'
  ) {
    throw new Error('AppearanceSettingsPanel requires the global appearance controller');
  }
  const selectedAccent = ACCENTS.find((option) => option.value === accent);

  if (!selectedAccent) {
    throw new Error(`Unknown appearance accent: ${accent}`);
  }

  return (
    <section className="appearance-settings panel" aria-labelledby="appearance-settings-title" data-testid="appearance-settings-panel">
      <header className="appearance-settings__header">
        <div>
          <p className="appearance-settings__eyebrow">{copy.eyebrow}</p>
          <h2 id="appearance-settings-title">{copy.title}</h2>
          <p>{copy.description}</p>
        </div>
        <div className="appearance-settings__header-actions">
          <span className="appearance-settings__local-badge">{copy.saved}</span>
          <button type="button" className="appearance-settings__reset" onClick={reset}>{copy.reset}</button>
        </div>
      </header>

      <div className="appearance-settings__layout" data-testid="appearance-responsive-layout">
        <div className="appearance-settings__controls">
          <fieldset aria-describedby={`${themeGroupId}-hint`}>
            <legend>{copy.theme}</legend>
            <p id={`${themeGroupId}-hint`}>{copy.themeHint}</p>
            <div className="appearance-settings__theme-options">
              {THEMES.map((value) => {
                const option = copy.themes[value];
                if (!option) throw new Error(`Missing appearance theme copy: ${value}`);
                return (
                <label className="appearance-settings__choice" key={value}>
                  <input
                    type="radio"
                    name={themeGroupId}
                    value={value}
                    checked={theme === value}
                    onChange={() => setThemeMode(value)}
                  />
                  <span aria-hidden="true" className={`appearance-settings__theme-icon is-${value}`} />
                  <span><strong>{option.label}</strong><small>{option.description}</small></span>
                </label>);
              })}
            </div>
          </fieldset>

          <fieldset aria-describedby={`${zoomGroupId}-hint`}>
            <legend>{copy.zoom}</legend>
            <p id={`${zoomGroupId}-hint`}>{copy.zoomHint}</p>
            <div className="appearance-settings__zoom-options">
              {ZOOM_LEVELS.map((level) => (
                <label key={level}>
                  <input
                    type="radio"
                    name={zoomGroupId}
                    value={level}
                    checked={zoom === level}
                    onChange={() => setUiScale(level)}
                  />
                  <span>{level}%</span>
                </label>
              ))}
            </div>
          </fieldset>

          <fieldset aria-describedby={`${accentGroupId}-hint`}>
            <legend>{copy.accent}</legend>
            <p id={`${accentGroupId}-hint`}>{copy.accentHint}</p>
            <div className="appearance-settings__accent-options">
              {ACCENTS.map((option) => (
                <label key={option.value} title={copy.accents[option.value]}>
                  <input
                    type="radio"
                    name={accentGroupId}
                    value={option.value}
                    aria-label={copy.accents[option.value]}
                    checked={accent === option.value}
                    disabled={option.disabled}
                    onChange={() => {
                      if (!option.disabled) setAccent(option.value);
                    }}
                  />
                  <span className="appearance-settings__swatch" style={{ '--swatch-color': option.color }} />
                </label>
              ))}
            </div>
          </fieldset>
        </div>

        <aside
          className={`appearance-settings__preview is-${theme}`}
          data-testid="appearance-preview"
          data-theme={theme}
          data-zoom={zoom}
          data-accent={accent}
          style={{ '--appearance-accent': selectedAccent.color }}
          aria-label={copy.previewLabel}
          aria-live="polite"
        >
          <div className="appearance-settings__preview-window">
            <div className="appearance-settings__preview-toolbar"><i /><i /><i /><span>{copy.previewTitle}</span></div>
            <div className="appearance-settings__preview-content" style={{ '--preview-scale': zoom / 100 }}>
              <nav aria-label={copy.previewNavigation}><span className="is-active">{copy.previewOverview}</span><span>{copy.previewTasks}</span><span>{copy.previewSettings}</span></nav>
              <main>
                <span className="appearance-settings__preview-kicker">{copy.previewProgress}</span>
                <strong>{copy.previewPrompt}</strong>
                <p>{copy.previewDescription}</p>
                <button type="button">{copy.previewAction}</button>
              </main>
            </div>
          </div>
          <dl className="appearance-settings__summary">
            <div><dt>{copy.theme}</dt><dd>{copy.themes[theme]?.label}</dd></div>
            <div><dt>{copy.zoom}</dt><dd>{zoom}%</dd></div>
            <div><dt>{copy.accent}</dt><dd>{copy.accents[selectedAccent.value]}</dd></div>
          </dl>
        </aside>
      </div>
    </section>
  );
}

export { AppearanceSettingsPanel };
