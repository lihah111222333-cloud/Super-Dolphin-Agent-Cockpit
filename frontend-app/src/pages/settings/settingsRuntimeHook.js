import { useCallback, useEffect, useRef, useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { APP_COPY } from '../../shared/i18n/appI18n.js';
import { firstPresentText } from '../shared/pageShared.js';
import { runBackgroundAction, runUIAction } from '../../shared/ui/runUIAction.js';
import { settingsPageService } from './services/settingsPageService.js';
import {
  changeActiveProviderPreference,
  defaultSettingsForm,
  normalizeSettingsCwd,
  readRuntimeSettingsForm,
  saveProviderRuntimePreferences,
  saveRuntimePreferences,
  settingsFormWithUpdate,
  textValue,
} from './settingsPageRuntime.js';

const { checkAppUpdate, getBuildInfo, installLatestAppUpdate } = settingsPageService;

function settingsRuntimePreferencesQueryKey(cwd) {
  return ['settings', 'runtime-preferences', normalizeSettingsCwd(cwd)];
}

function useSettingsRuntime(cwd, copy) {
  const [buildInfo, setBuildInfo] = useState(null);
  const [form, setForm] = useState(defaultSettingsForm);
  const [status, setStatus] = useState('');
  const [error, setError] = useState('');
  const [updateInfo, setUpdateInfo] = useState(null);
  const [updateInstalled, setUpdateInstalled] = useState(false);
  const [updateNotice, setUpdateNotice] = useState({ level: 'info', message: '' });
  const preferenceRequestSeq = useRef(0);
  const nextPreferenceRequest = useCallback(() => {
    preferenceRequestSeq.current += 1;
    const requestSeq = preferenceRequestSeq.current;
    return () => preferenceRequestSeq.current === requestSeq;
  }, []);
  const loadBuildInfo = useCallback(async () => {
    setError('');
    try {
      const info = await getBuildInfo();
      if (!info || typeof info !== 'object') throw new Error('build info response must be an object');
      setBuildInfo(info);
      setStatus(copy.buildInfoRefreshed);
    } catch (err) {
      setError('读取构建信息失败，请重试。');
      throw err;
    }
  }, [copy]);
  const refreshBuildInfo = useCallback(() => runUIAction('settings.build.refresh', loadBuildInfo, { retryable: true }), [loadBuildInfo]);
  const runtimePreferencesQuery = useQuery({
    queryKey: settingsRuntimePreferencesQueryKey(cwd),
    queryFn: () => runBackgroundAction('settings.runtime.bootstrap', () => readRuntimeSettingsForm(cwd)),
    enabled: Boolean(normalizeSettingsCwd(cwd)),
    retry: false,
    refetchOnWindowFocus: false,
  });
  const updateForm = useCallback((key) => (event) => {
    const value = event.target.type === 'checkbox' ? event.target.checked : event.target.value;
    setForm((current) => settingsFormWithUpdate(current, key, value));
  }, []);
  const changeActiveProvider = useCallback((event) => runUIAction('settings.provider.change', () => changeActiveProviderPreference({ copy, cwd, event, isCurrent: nextPreferenceRequest(), setError, setForm, setStatus })), [copy, cwd, nextPreferenceRequest]);
  const saveRuntimeSettings = useCallback(() => runUIAction('settings.runtime.save', () => saveRuntimePreferences({ copy, cwd, form, setError, setStatus })), [copy, cwd, form]);
  const saveProviderSettings = useCallback(() => runUIAction('settings.provider.save', () => saveProviderRuntimePreferences({ copy, cwd, form, setError, setStatus })), [copy, cwd, form]);
  const checkUpdateMutation = useMutation({
    mutationFn: checkAppUpdate,
    onMutate: () => {
      setUpdateInstalled(false);
      setUpdateInfo(null);
      setUpdateNotice({ level: 'info', message: copy.update.checking });
    },
    onSuccess: (info) => {
      applyCheckedAppUpdateInfo({ copy, info, setUpdateInfo, setUpdateNotice });
    },
    onError: (_mutationError) => {
      setUpdateInfo(null);
      setUpdateNotice({ level: 'error', message: copy.update.checkFailed });
    },
    retry: false,
  });
  const installUpdateMutation = useMutation({
    mutationFn: installLatestAppUpdate,
    onMutate: ({ pendingInfo }) => {
      const installingMessage = appUpdateInstallingMessage(pendingInfo, copy);
      setUpdateInfo(null);
      setUpdateNotice({ level: 'info', message: installingMessage });
      return { installingMessage, pendingInfo };
    },
    onSuccess: (_payload, _variables, context) => {
      setUpdateInstalled(true);
      setUpdateNotice({ level: 'info', message: context?.installingMessage || copy.update.installing });
    },
    onError: (_mutationError, _variables, context) => {
      setUpdateInfo(context?.pendingInfo || null);
      setUpdateInstalled(false);
      setUpdateNotice({ level: 'error', message: copy.update.installFailed });
    },
    retry: false,
  });
  const updateBusy = checkUpdateMutation.isPending;
  const updateInstalling = installUpdateMutation.isPending || updateInstalled;
  const checkForUpdate = useCallback(() => {
    if (updateBusy || updateInstalling) return;
    return runUIAction('app.update.check', () => checkUpdateMutation.mutateAsync(), { retryable: true });
  }, [checkUpdateMutation, updateBusy, updateInstalling]);
  const installUpdate = useCallback(() => {
    if (!updateInfo?.available || updateInstalling) return;
    return runUIAction('app.update.install', () => installUpdateMutation.mutateAsync({ pendingInfo: updateInfo }));
  }, [installUpdateMutation, updateInfo, updateInstalling]);
  useEffect(() => { runBackgroundAction('settings.build.bootstrap', loadBuildInfo); }, [loadBuildInfo]);
  useEffect(() => {
    if (runtimePreferencesQuery.error) {
      setError('读取运行时偏好失败，请重试。');
      return;
    }
    if (runtimePreferencesQuery.data) {
      setError('');
      setForm(runtimePreferencesQuery.data);
    }
  }, [runtimePreferencesQuery.data, runtimePreferencesQuery.error]);
  return { buildInfo, changeActiveProvider, checkForUpdate, error, form, installUpdate, refreshBuildInfo, saveProviderSettings, saveRuntimeSettings, status, updateBusy, updateInfo, updateInstalling, updateNotice, updateForm };
}

function applyCheckedAppUpdateInfo({ copy, info, setUpdateInfo, setUpdateNotice }) {
  const updateCopy = copy.update;
  if (info?.enabled === false) {
    setUpdateInfo(null);
    setUpdateNotice({ level: 'warning', message: updateCopy.disabled });
  } else if (info?.available) {
    setUpdateInfo(info);
    setUpdateNotice({ level: 'info', message: updateCopy.found + ' ' + appUpdateVersionLabel(info, copy) });
  } else {
    setUpdateInfo(null);
    setUpdateNotice({ level: 'info', message: updateCopy.latest });
  }
}

function appUpdateVersionLabel(info, copy = APP_COPY.zh.settings) {
  const version = appUpdateConcreteVersionLabel(info) || copy.update.availableUpdate;
  const platform = firstPresentText(info?.platform, info?.artifact?.platform);
  return platform ? `${version} (${platform})` : version;
}

function appUpdateInstallingMessage(info, copy = APP_COPY.zh.settings) {
  const version = appUpdateConcreteVersionLabel(info);
  if (!version) return copy.update.installing;
  const platform = firstPresentText(info?.platform, info?.artifact?.platform);
  return copy.update.installing + ' ' + (platform ? `${version} (${platform})` : version);
}

function appUpdateConcreteVersionLabel(info) {
  return firstPresentText(info?.version, info?.latestVersion, info?.latest_version);
}

function appUpdateCurrentVersionLabel(buildInfo) {
  const packagedVersion = firstPresentText(buildInfo?.appVersion, buildInfo?.app_version, buildInfo?.updateVersion, buildInfo?.update_version);
  if (packagedVersion) return appUpdateDisplayVersion(packagedVersion);
  return firstPresentText(buildInfo?.version, 'unknown');
}

function appUpdateDisplayVersion(version) {
  const value = textValue(version).trim();
  if (!value) return '';
  if (/^[0-9]+(?:\.[0-9]+){1,2}(?:[-+].*)?$/.test(value)) return `v${value}`;
  return value;
}

export { appUpdateCurrentVersionLabel, settingsRuntimePreferencesQueryKey, useSettingsRuntime };
