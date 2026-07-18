export function productionActionFailureMatrixTitle(index, { actionId, errorSource }) {
	if (actionId === 'provider.reconnect' && errorSource === 'promise-reject') {
		return 'routes provider reconnect cancellation to Health without an interactive error';
	}
	return `cell-${index}`;
}
