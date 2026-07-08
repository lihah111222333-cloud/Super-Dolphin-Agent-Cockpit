import React from 'react';

function UILogRefreshButton({ logsCopy, onRefresh, refreshing }) {
  return (
    <div className="settings-action-row settings-log-action-row">
      <button
        type="button"
        className="btn btn-secondary btn-toolbar-sm"
        data-testid="settings-log-refresh-button"
        onClick={() => { void onRefresh(); }}
        disabled={refreshing}
      >
        {refreshing ? logsCopy.refreshing : logsCopy.refresh}
      </button>
    </div>
  );
}

export { UILogRefreshButton };
