import { expect, it } from 'vitest';
import actionProducerRegistry from '../../../config/action-producer-registry.json';
import actionProducerTestMatrix from '../../../config/action-producer-test-matrix.json';
import { discoverActionProducers } from '../../../scripts/action-producer-guard.mjs';
import {
	diagnosticCauseForTest,
	frontendHealthSnapshot,
	resetFrontendHealthForTest,
} from '../diagnostics/frontendHealthStore.js';
import {
	resetVisibleActionFailureForTest,
	visibleActionFailureSnapshot,
} from './actionFailureSink.js';
import { runBackgroundAction, runUIAction } from './runUIAction.js';

const cellKey = (actionId, errorSource) => `${actionId}\n${errorSource}`;

function duplicateValues(values) {
	const seen = new Set();
	const duplicates = new Set();
	for (const value of values) {
		if (seen.has(value)) duplicates.add(value);
		seen.add(value);
	}
	return [...duplicates].sort();
}

function assertProductionRegistryIsExact() {
	const discovery = discoverActionProducers();
	const entries = actionProducerRegistry.coveredProducers;
	const registeredIds = entries.map((entry) => entry.actionId);
	const discoveredIds = [...discovery.counts.keys()];

	expect(discovery.problems, 'production action discovery must be valid').toEqual([]);
	expect(discoveredIds.length, 'production action discovery must not be zero').toBeGreaterThan(0);
	expect(entries.length, 'action producer registry must not be zero').toBeGreaterThan(0);
	expect(duplicateValues(registeredIds), 'action producer registry contains duplicate actionIds').toEqual([]);
	expect(
		discoveredIds.filter((actionId) => !registeredIds.includes(actionId)).sort(),
		'missing action producer registry entries',
	).toEqual([]);
	expect(
		registeredIds.filter((actionId) => !discovery.counts.has(actionId)).sort(),
		'stale action producer registry entries',
	).toEqual([]);

	for (const entry of entries) {
		expect(entry.producerCount, `${entry.actionId} producerCount must be positive`).toBeGreaterThan(0);
		expect(discovery.counts.get(entry.actionId), `${entry.actionId} producer count drifted`).toBe(entry.producerCount);
		expect(discovery.kinds.get(entry.actionId), `${entry.actionId} producer kind drifted`).toBe(entry.kind);
		expect(entry.errorSources.length, `${entry.actionId} errorSources must not be zero`).toBeGreaterThan(0);
		expect(entry.healthSink, `${entry.actionId} must declare the production Health sink`).toBe('frontendHealthStore');
		expect(entry.visibleSink, `${entry.actionId} visible sink declaration drifted`).toBe(
			entry.kind === 'user' ? 'ActionFailureSink' : null,
		);
	}

	return entries;
}

function assertMatrixIsExact(entries) {
	const cells = actionProducerTestMatrix.cells;
	expect(cells.length, 'action producer error matrix must not be zero').toBeGreaterThan(0);

	const expectedKeys = entries.flatMap((entry) => (
		entry.errorSources.map((errorSource) => cellKey(entry.actionId, errorSource))
	));
	const actualKeys = cells.map((cell) => cellKey(cell.actionId, cell.errorSource));

	expect(duplicateValues(actualKeys), 'action producer error matrix contains duplicate cells').toEqual([]);
	expect(
		expectedKeys.filter((key) => !actualKeys.includes(key)).sort(),
		'missing production action producer error cells',
	).toEqual([]);
	expect(
		actualKeys.filter((key) => !expectedKeys.includes(key)).sort(),
		'stale production action producer error cells',
	).toEqual([]);
	return cells;
}

function failureCause(actionId, errorSource) {
	if (actionId === 'provider.reconnect' && errorSource === 'promise-reject') {
		const cause = new Error('provider cancelled upstream without a user cancellation');
		cause.name = 'ProviderCancellationError';
		return cause;
	}
	if (errorSource === 'invalid-response') return new TypeError(`${actionId} returned an invalid response`);
	return new Error(`${actionId} failed through ${errorSource}`);
}

function failingAction(errorSource, cause) {
	if (errorSource === 'sync-throw') return () => { throw cause; };
	if (errorSource === 'unsuccessful-result') return () => false;
	return () => Promise.reject(cause);
}

async function enforceDeclaredSink(entry, errorSource) {
	window.localStorage.clear();
	resetFrontendHealthForTest();
	resetVisibleActionFailureForTest();

	const cause = failureCause(entry.actionId, errorSource);
	const action = failingAction(errorSource, cause);
	if (entry.kind === 'background') {
		runBackgroundAction(entry.actionId, action);
	} else {
		runUIAction(entry.actionId, action, { rejectFalse: errorSource === 'unsuccessful-result' });
	}
	await Promise.resolve();
	await Promise.resolve();

	const healthRecord = frontendHealthSnapshot().find((record) => record.actionId === entry.actionId);
	expect(healthRecord, `${entry.actionId} x ${errorSource} did not reach frontendHealthStore`).toBeDefined();
	const retainedCause = diagnosticCauseForTest(healthRecord.diagnosticId);
	if (errorSource === 'unsuccessful-result') {
		expect(retainedCause, `${entry.actionId} unsuccessful result cause was not retained`).toBeInstanceOf(TypeError);
	} else {
		expect(retainedCause, `${entry.actionId} x ${errorSource} retained the wrong cause`).toBe(cause);
	}

	const visibleFailure = visibleActionFailureSnapshot();
	if (entry.visibleSink === 'ActionFailureSink') {
		expect(visibleFailure, `${entry.actionId} x ${errorSource} did not reach ActionFailureSink`).toEqual(
			expect.objectContaining({ actionId: entry.actionId }),
		);
	} else {
		expect(visibleFailure, `${entry.actionId} x ${errorSource} leaked a background failure to the visible sink`).toBeNull();
	}

	if (entry.actionId === 'provider.reconnect' && errorSource === 'promise-reject') {
		expect(healthRecord.actionId).toBe('provider.reconnect');
		expect(visibleFailure, 'background provider cancellation must remain Health-only').toBeNull();
	}
}

const productionEntries = assertProductionRegistryIsExact();
const productionCells = assertMatrixIsExact(productionEntries);
const entriesByActionId = new Map(productionEntries.map((entry) => [entry.actionId, entry]));

it.each(productionCells.map((cell, index) => [index, cell]))('cell-%s', async (_index, { actionId, errorSource }) => {
	const entry = entriesByActionId.get(actionId);
	expect(entry, `matrix cell references missing producer ${actionId}`).toBeDefined();
	await enforceDeclaredSink(entry, errorSource);
});
