export function runUIAction(action, options = {}) {
  const { onError, logger = console.error } = options;
  const reportError = (error) => {
    if (typeof logger === 'function') logger('[frontend-app] UI action failed', error);
    if (typeof onError === 'function') onError(error);
  };

  try {
    const result = typeof action === 'function' ? action() : action;
    if (result && typeof result.catch === 'function') {
      void result.catch(reportError);
    }
  }
  catch (error) {
    reportError(error);
  }
}
