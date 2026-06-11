import React, { useCallback, useEffect, useState } from 'react';

function VideoSettingsCard({ getApiKey, setApiKey: saveApiKey }) {
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
      setNotice({ level: 'error', message: '读取视频 API Key 失败：' + (err?.message || String(err)) });
    });
  }, [getApiKey]);

  const save = useCallback(async () => {
    const key = apiKey.trim();
    if (!key) {
      setNotice({ level: 'error', message: '请输入 API Key' });
      return;
    }
    try {
      await saveApiKey({ apiKey: key });
      setConfigured(true);
      setMasked(key.length > 8 ? key.slice(0, 4) + '*'.repeat(key.length - 8) + key.slice(-4) : '*'.repeat(key.length));
      setApiKey('');
      setNotice({ level: 'info', message: '已保存' });
    } catch (err) {
      setNotice({ level: 'error', message: '保存失败：' + (err?.message || String(err)) });
    }
  }, [apiKey, saveApiKey]);

  return (
    <>
      <div className="section-header">视频生成（硅基流动 Wan2.2）</div>
      <div className="data-card-vue" data-testid="settings-video-card">
        <div className="data-row-vue">
          <strong>SiliconFlow API Key</strong>
          <span>{configured ? masked : '未配置'}</span>
        </div>
        <div className="settings-stall-row">
          <label className="settings-stall-label" htmlFor="settings-sf-key">API Key</label>
          <input id="settings-sf-key" className="settings-stall-input" type="password" placeholder="sk-..." value={apiKey} onChange={(event) => setApiKey(event.target.value)} />
        </div>
        <div className="settings-action-row">
          <button className="btn btn-primary" type="button" onClick={save}>保存</button>
          {notice ? <span className="settings-page-notice" data-testid="settings-video-notice" role={notice.level === 'error' ? 'alert' : 'status'}>{notice.message}</span> : null}
        </div>
      </div>
    </>
  );
}

export { VideoSettingsCard };
