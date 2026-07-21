/**
 * Adds the local store warning after runUIAction has published its public error.
 * This callback deliberately does not replace or suppress the global failure path.
 * @param {{ addWarning?: (level: string, event: string, details: { error: string }) => unknown } | undefined} store
 */
export function uiActionWarningOptions(store) {
  return {
    onError: (publicError) => {
      store?.addWarning?.('error', 'ui.action.failed', { error: publicError.message });
    },
  };
}
