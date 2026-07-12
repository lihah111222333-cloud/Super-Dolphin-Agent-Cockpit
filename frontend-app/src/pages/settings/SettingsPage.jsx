import React from 'react';
import { Menu, Settings } from 'lucide-react';
import { useClientStore } from '../../entities/client/model/useClientStore.js';
import { PageHeader } from '../shared/pageComponents.jsx';
import { BuiltinToolsCard } from './components/BuiltinToolsCard.jsx';
import { ModelProvidersCard } from './components/ModelProvidersCard.jsx';
import { ProviderPropertiesCard, ProviderSettingsPanel } from './components/ProviderSettingsPanels.jsx';
import { PromptSettingsCard } from './components/PromptSettingsCard.jsx';
import { AboutPanel, RuntimeSettingsPanels } from './components/SettingsSystemPanels.jsx';
import { UILogCard } from './components/UILogCard.jsx';
import { VideoSettingsCard } from './components/VideoSettingsCard.jsx';
import { APP_BRAND_NAME, APP_COPY } from '../../shared/i18n/appI18n.js';
import { ShortcutSettingsCard } from '../../features/shortcut-settings/ui/ShortcutSettingsCard.jsx';
import { settingsPageService } from './services/settingsPageService.js';
import { useBuiltinToolsSettings } from './settingsBuiltinToolsRuntime.js';
import { PROVIDER_LABELS, loadSettingsDashboardLogs, normalizeSettingsCwd, providerSettingsViewConfig, textValue } from './settingsPageRuntime.js';
import { useProviderPreferences } from './settingsProviderPreferencesRuntime.js';
import { usePromptSettings } from './settingsPromptRuntime.js';
import { appUpdateCurrentVersionLabel, useSettingsRuntime } from './settingsRuntimeHook.js';
import './SettingsPage.css';

const { getVideoApiKey, setVideoApiKey } = settingsPageService;

function SettingsPage({ copy = APP_COPY.zh.settings, projectPath, shortcutController }) {
  const store = useClientStore();
  const cwd = normalizeSettingsCwd(projectPath) || normalizeSettingsCwd(store.activeProject) || normalizeSettingsCwd(store.cwd);
  const runtime = useSettingsRuntime(cwd, copy);
  const provider = useProviderPreferences(cwd, runtime.form.activeProvider, copy);
  const prompt = usePromptSettings(cwd, copy);
  const builtins = useBuiltinToolsSettings(cwd, copy);
  return <SettingsPageView builtins={builtins} copy={copy} cwd={cwd} prompt={prompt} provider={provider} runtime={runtime} shortcutController={shortcutController} store={store} />;
}

function mobileAccountName(cwd, fallback = '本地用户') {
  const parts = textValue(cwd).split(/[\\/]/).filter(Boolean);
  const last = parts.at(-1);
  return last ? last : fallback;
}

function MobileAccountPanel({ copy = APP_COPY.zh.settings, cwd, runtime }) {
  const accountName = mobileAccountName(cwd, copy.accountNameFallback);
  const provider = PROVIDER_LABELS[runtime.form.activeProvider] || runtime.form.activeProvider || 'Codex';
  return (
    <section className="settings-mobile-account" data-testid="settings-mobile-account" aria-label={copy.mobileAccount}>
      <header>
        <button type="button" aria-label={copy.menu} disabled><Menu size={18} /></button>
        <h2>{APP_BRAND_NAME}</h2>
        <div className="settings-mobile-avatar" aria-label={copy.avatar}>SY</div>
      </header>
      <div className="settings-mobile-card">
        <span>{copy.username}</span>
        <strong>{accountName}</strong>
        <small>{cwd || copy.noProject}</small>
      </div>
      <div className="settings-mobile-card">
        <span>{copy.account}</span>
        <strong>{provider}</strong>
        <small>{copy.accountDescription}</small>
      </div>
      <div className="settings-mobile-card">
        <span>{copy.settings}</span>
        <strong>{copy.runtimeConfig}</strong>
        <small>{copy.runtimeDescription}</small>
      </div>
      <div className="settings-mobile-card is-disabled">
        <span>{copy.logout}</span>
        <strong>{copy.authPending}</strong>
        <button type="button" data-testid="settings-mobile-logout-button" disabled>{copy.logoutTab}</button>
      </div>
      <nav className="settings-mobile-tabs" aria-label={copy.mobileAccount}>
        <button type="button" disabled>{copy.accountTab}</button>
        <button type="button" disabled>{copy.settingsTab}</button>
        <button type="button" disabled>{copy.logoutTab}</button>
      </nav>
    </section>
  );
}

function SettingsPageView(props) {
  const { builtins, copy = APP_COPY.zh.settings, cwd, prompt, provider, runtime, shortcutController, store } = props;
  return (
    <section className="settings-page" data-testid="settings-page">
      <PageHeader icon={Settings} title={copy.title} actions={<button className="btn btn-secondary" type="button" data-testid="settings-refresh-build-button" onClick={() => void runtime.refreshBuildInfo()}>{copy.refreshBuildInfo}</button>} />
      <MobileAccountPanel copy={copy} cwd={cwd} runtime={runtime} />
      <SettingsNotices error={runtime.error} status={runtime.status} />
      <div className="panel-body" data-testid="settings-panel-body">
        <AboutPanel buildInfo={runtime.buildInfo} copy={copy} cwd={cwd} runtime={runtime} updateCurrentVersion={appUpdateCurrentVersionLabel(runtime.buildInfo)} />
        <RuntimeSettingsPanels copy={copy} runtime={runtime} />
        <ProviderSettingsPanel copy={copy} runtime={runtime} viewConfig={providerSettingsViewConfig} />
        <ProviderPropertiesCard copy={copy} provider={provider} />
        <ModelProvidersCard copy={copy} cwd={cwd} />
        <PromptSettingsCard copy={copy} prompt={prompt} />
        {shortcutController ? <ShortcutSettingsCard controller={shortcutController} copy={copy.shortcuts} /> : null}
        <BuiltinToolsCard builtins={builtins} copy={copy} />
        <VideoSettingsCard copy={copy} getApiKey={getVideoApiKey} setApiKey={setVideoApiKey} />
        <UILogCard copy={copy} loadLogs={loadSettingsDashboardLogs} store={store} />
      </div>
    </section>
  );
}

function SettingsNotices({ error, status }) {
  return (
    <>
      {status ? <output className="settings-page-notice settings-status">{status}</output> : null}
      {error ? <p className="settings-page-notice danger-text" role="alert">{error}</p> : null}
    </>
  );
}

export { SettingsPage };
