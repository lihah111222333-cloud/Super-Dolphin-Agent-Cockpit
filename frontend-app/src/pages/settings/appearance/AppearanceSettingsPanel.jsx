import React, { useId } from 'react';
import './AppearanceSettingsPanel.css';

const THEMES = [
  { value: 'system', label: '跟随系统', description: '自动匹配设备外观' },
  { value: 'light', label: '浅色', description: '始终使用明亮界面' },
  { value: 'dark', label: '深色', description: '始终使用深色界面' },
];

const ZOOM_LEVELS = [80, 90, 100, 110, 125, 150];

const ACCENTS = [
  { value: 'violet', label: '星云紫', color: '#7c5cff' },
  { value: 'blue', label: '湖光蓝', color: '#2f7df4' },
  { value: 'mint', label: '薄荷绿', color: '#21a179' },
  { value: 'amber', label: '暖阳橙', color: '#e9812d' },
  { value: 'rose', label: '珊瑚红', color: '#df5b78' },
  { value: 'custom', label: '自定义色（即将推出）', color: '#8d94a6', disabled: true },
];

function AppearanceSettingsPanel({ appearance }) {
  const themeGroupId = useId();
  const zoomGroupId = useId();
  const accentGroupId = useId();
  if (!appearance || typeof appearance !== 'object') {
    throw new Error('AppearanceSettingsPanel requires the global appearance controller');
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
          <p className="appearance-settings__eyebrow">个性化</p>
          <h2 id="appearance-settings-title">外观与显示</h2>
          <p>外观会即时应用到整个工作台，并安全保存在当前浏览器。</p>
        </div>
        <div className="appearance-settings__header-actions">
          <span className="appearance-settings__local-badge">全局 · 已保存</span>
          <button type="button" className="appearance-settings__reset" onClick={reset}>恢复默认</button>
        </div>
      </header>

      <div className="appearance-settings__layout" data-testid="appearance-responsive-layout">
        <div className="appearance-settings__controls">
          <fieldset aria-describedby={`${themeGroupId}-hint`}>
            <legend>主题</legend>
            <p id={`${themeGroupId}-hint`}>选择预览中的明暗外观</p>
            <div className="appearance-settings__theme-options">
              {THEMES.map((option) => (
                <label className="appearance-settings__choice" key={option.value}>
                  <input
                    type="radio"
                    name={themeGroupId}
                    value={option.value}
                    checked={theme === option.value}
                    onChange={() => setThemeMode(option.value)}
                  />
                  <span aria-hidden="true" className={`appearance-settings__theme-icon is-${option.value}`} />
                  <span><strong>{option.label}</strong><small>{option.description}</small></span>
                </label>
              ))}
            </div>
          </fieldset>

          <fieldset aria-describedby={`${zoomGroupId}-hint`}>
            <legend>界面缩放</legend>
            <p id={`${zoomGroupId}-hint`}>调整预览内容的显示比例</p>
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
            <legend>强调色</legend>
            <p id={`${accentGroupId}-hint`}>用于预览中的选中状态和主要操作</p>
            <div className="appearance-settings__accent-options">
              {ACCENTS.map((option) => (
                <label key={option.value} title={option.label}>
                  <input
                    type="radio"
                    name={accentGroupId}
                    value={option.value}
                    aria-label={option.label}
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
          aria-label="外观实时预览"
          aria-live="polite"
        >
          <div className="appearance-settings__preview-window">
            <div className="appearance-settings__preview-toolbar"><i /><i /><i /><span>工作台预览</span></div>
            <div className="appearance-settings__preview-content" style={{ '--preview-scale': zoom / 100 }}>
              <nav aria-label="预览导航"><span className="is-active">概览</span><span>任务</span><span>设置</span></nav>
              <main>
                <span className="appearance-settings__preview-kicker">今日进度</span>
                <strong>准备好继续创作了吗？</strong>
                <p>主题、缩放和强调色会即时显示在这里。</p>
                <button type="button">新建任务</button>
              </main>
            </div>
          </div>
          <dl className="appearance-settings__summary">
            <div><dt>主题</dt><dd>{THEMES.find((option) => option.value === theme)?.label}</dd></div>
            <div><dt>缩放</dt><dd>{zoom}%</dd></div>
            <div><dt>强调色</dt><dd>{selectedAccent.label}</dd></div>
          </dl>
        </aside>
      </div>
    </section>
  );
}

export { AppearanceSettingsPanel };
