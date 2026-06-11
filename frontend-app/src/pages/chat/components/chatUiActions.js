function runUIAction(action) {
  try {
    const result = typeof action === 'function' ? action() : action;
    if (result && typeof result.catch === 'function') {
      void result.catch(() => {});
    }
  }
  catch (error) {
    void error;
  }
}

export { runUIAction };
