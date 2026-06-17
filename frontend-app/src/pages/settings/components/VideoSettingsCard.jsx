import React, { useCallback, useEffect, useState } from 'react';

function VideoSettingsCard({ copy, getApiKey, setApiKey: saveApiKey }) {
  const videoCopy = copy.video;
  const [apiKey, setApiKey] = useState('');
  const [notice, setNotice] = useState(null);
  const [configured, setConfigured] = useState(false);
  const [masked, setMasked] = useState('');

  useEffect(() => {
    getApiKey().then((res) => {
      if (res?.configured) {
        setConfigured(true);
        setMasked(res.masked);
      }
    }).catch((err) => {
      setNotice({ level: 'error', message: videoCopy.readFailed + (err?.message || String(err)) });
    });
  }, [getApiKey, videoCopy]);

  const save = useCallback(async () => {
    const key = apiKey.trim();
    if (!key) {
      setNotice({ level: 'error', message: videoCopy.apiKeyRequired });
      return;
    }
    try {
      await saveApiKey({ apiKey: key });
      setConfigured(true);
      setMasked(key.length > 8 ? key.slice(0, 4) + '*'.repeat(key.length - 8) + key.slice(-4) : '*'.repeat(key.length));
      setApiKey('');
      setNotice({ level: 'info', message: videoCopy.saved });
    } catch (err) {
      setNotice({ level: 'error', message: videoCopy.saveFailed + (err?.message || String(err)) });
    }
  }, [apiKey, saveApiKey, videoCopy]);

  return (
    <>
      <div className="section-header">{videoCopy.title}</div>
      <div className="data-card-vue" data-testid="settings-video-card">
        <div className="data-row-vue">
          <strong>SiliconFlow API Key</strong>
          <span>{configured ? masked : videoCopy.notConfigured}</span>
        </div>
        <div className="settings-stall-row">
          <label className="settings-stall-label" htmlFor="settings-sf-key">API Key</label>
          <input id="settings-sf-key" className="settings-stall-input" type="password" placeholder="sk-..." value={apiKey} onChange={(event) => setApiKey(event.target.value)} />
        </div>
        <div className="settings-action-row">
          <button className="btn btn-primary" type="button" onClick={save}>{videoCopy.save}</button>
          {notice ? <span className="settings-page-notice" data-testid="settings-video-notice" role={notice.level === 'error' ? 'alert' : 'status'}>{notice.message}</span> : null}
        </div>
      </div>
    </>
  );
}

export { VideoSettingsCard };
