import { describe, expect, it } from 'vitest';
import { productionActionFailureMatrixTitle } from './productionActionFailureMatrixTitles.js';

describe('productionActionFailureMatrixTitle', () => {
	it('keeps ordinary matrix evidence bound to its cell index', () => {
		expect(productionActionFailureMatrixTitle(17, {
			actionId: 'thread.pin',
			errorSource: 'sync-throw',
		})).toBe('cell-17');
	});

	it('keeps the provider reconnect cancellation evidence discoverable by its exact name', () => {
		expect(productionActionFailureMatrixTitle(42, {
			actionId: 'provider.reconnect',
			errorSource: 'promise-reject',
		})).toBe('routes provider reconnect cancellation to Health without an interactive error');
	});
});
