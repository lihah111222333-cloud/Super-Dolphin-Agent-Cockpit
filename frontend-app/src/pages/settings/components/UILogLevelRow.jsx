import React from 'react';

function UILogLevelRow({ logsCopy, store }) {
  return (
    <div className="settings-stall-row settings-log-control-row">
      <label className="settings-stall-label" htmlFor="settings-log-level-select">{logsCopy.level}</label>
      <select id="settings-log-level-select" className="settings-stall-input settings-log-level-select" data-testid="settings-log-level-select" value={store.logLevel} onChange={(event) => store.setLogLevel(event.target.value)}>
        <option value="debug">{logsCopy.debug}</option><option value="info">{logsCopy.info}</option><option value="warn">{logsCopy.warn}</option><option value="error">{logsCopy.error}</option>
      </select>
      <span className="settings-stall-unit">{logsCopy.live}</span>
    </div>
  );
}

export { UILogLevelRow };
