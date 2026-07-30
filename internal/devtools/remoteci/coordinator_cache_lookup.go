package remoteci

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

type remoteWorkloadCacheLookup struct {
	snapshot             *remoteGitTreeSnapshot
	workerWorkloads      []gate.Workload
	inputDigests         map[string]string
	cacheEntries         []remoteWorkloadCacheEntry
	packageLegacyEntries []remoteWorkloadCacheEntry
	resume               remoteGoTestResumeSet
}

func (coordinator *Coordinator) runWithWorkloadCache(
	ctx context.Context,
	input RunInput,
	plan gate.GatePlan,
	catalog gate.WorkloadCatalog,
	jobID string,
	tempRoot string,
	objectKeys *[]string,
	createdGroups *[]string,
	result RunResult,
	trace *remoteRunPerformanceTrace,
) (RunResult, error) {
	selection, err := coordinator.lookupPassedWorkloads(ctx, input, catalog, trace)
	if err != nil {
		return result, err
	}
	catalogProjectionCounts := remoteCIPhaseCounts{
		workloads:   len(selection.workloads),
		cacheHits:   len(selection.reused),
		cacheMisses: len(selection.workloads) - len(selection.reused),
	}
	catalogProjectionSpan := trace.start("catalog.project", catalogProjectionCounts)
	effectiveCatalogDigest, err := gate.WorkloadCatalogDigest(selection.catalog)
	trace.finish(catalogProjectionSpan, err, catalogProjectionCounts)
	if err != nil {
		return result, err
	}
	if input.LedgerStore != nil {
		catalogRecordCounts := remoteCIPhaseCounts{workloads: len(selection.workloads)}
		catalogRecordSpan := trace.start("ledger.catalog_record_effective", catalogRecordCounts)
		if err := input.LedgerStore.RecordWorkloadCatalog(
			selection.catalog,
			gate.WorkloadCatalogObservation{
				SourceTreeSHA: input.Tree,
				Entrypoint:    result.Entrypoint,
				Profile:       result.Profile,
				ObservedAt:    result.StartedAt,
			},
		); err != nil {
			trace.finish(catalogRecordSpan, err, catalogRecordCounts)
			return result, err
		}
		trace.finish(catalogRecordSpan, nil, catalogRecordCounts)
	}
	result.CatalogDigest = effectiveCatalogDigest
	result.ReusedWorkloads = selection.reused
	if len(selection.reused) == len(selection.workloads) {
		aggregateCounts := remoteCIPhaseCounts{
			workloads: len(selection.workloads),
			cacheHits: len(selection.reused),
		}
		aggregateSpan := trace.start("result.aggregate_cached", aggregateCounts)
		result, err = coordinator.completeRemoteRun(selection.catalog, input, nil, selection.cached, result)
		trace.finish(aggregateSpan, err, aggregateCounts)
		return result, err
	}
	return coordinator.runCacheMissWorkloads(
		ctx, input, plan, selection.catalog, jobID, tempRoot, selection.workloads, selection.cacheEntries,
		selection.cached, selection.reused, selection.goTestEntriesByParent,
		objectKeys, createdGroups, result, trace,
	)
}

// lookupPassedWorkloads 在分片规划前按固定顺序完成缓存查询。
func lookupPassedWorkloads(
	ctx context.Context,
	store ObjectStore,
	cachePrefix string,
	now func() time.Time,
	input RunInput,
	catalog gate.WorkloadCatalog,
	trace *remoteRunPerformanceTrace,
) (remoteWorkloadCacheSelection, error) {
	parentWorkloadCount := len(remoteShardableWorkloads(catalog))
	parentPrepareCounts := remoteCIPhaseCounts{workloads: parentWorkloadCount}
	parentPrepareSpan := trace.start("cache.parent_prepare", parentPrepareCounts)
	lookup, err := prepareRemoteWorkloadCacheLookup(ctx, cachePrefix, now, input, catalog)
	trace.finish(parentPrepareSpan, err, parentPrepareCounts)
	if err != nil {
		return remoteWorkloadCacheSelection{}, err
	}
	parentCounts := remoteCIPhaseCounts{workloads: len(lookup.workerWorkloads)}
	parentExactSpan := trace.start("cache.parent_exact", parentCounts)
	allCached, err := lookupExactPassedWorkloads(ctx, store, now, input, lookup)
	parentCounts.cacheHits = len(allCached)
	parentCounts.cacheMisses = len(lookup.workerWorkloads) - len(allCached)
	trace.finish(parentExactSpan, err, parentCounts)
	if err != nil {
		return remoteWorkloadCacheSelection{}, err
	}
	parentCompatibleSpan := trace.start("cache.parent_compatible", parentCounts)
	if err := promoteCompatiblePassedWorkloads(ctx, store, now, input, lookup, allCached); err != nil {
		parentCounts.cacheHits = len(allCached)
		parentCounts.cacheMisses = len(lookup.workerWorkloads) - len(allCached)
		trace.finish(parentCompatibleSpan, err, parentCounts)
		return remoteWorkloadCacheSelection{}, err
	}
	parentCounts.cacheHits = len(allCached)
	parentCounts.cacheMisses = len(lookup.workerWorkloads) - len(allCached)
	trace.finish(parentCompatibleSpan, nil, parentCounts)
	parentObjectSpan := trace.start("cache.parent_object_fallback", parentCounts)
	if err := loadUnknownPassedWorkloadsFromOSS(ctx, store, now, input, lookup, allCached); err != nil {
		parentCounts.cacheHits = len(allCached)
		parentCounts.cacheMisses = len(lookup.workerWorkloads) - len(allCached)
		trace.finish(parentObjectSpan, err, parentCounts)
		return remoteWorkloadCacheSelection{}, err
	}
	parentCounts.cacheHits = len(allCached)
	parentCounts.cacheMisses = len(lookup.workerWorkloads) - len(allCached)
	trace.finish(parentObjectSpan, nil, parentCounts)
	childCounts := remoteCIPhaseCounts{
		cacheHits:   len(allCached),
		cacheMisses: len(lookup.workerWorkloads) - len(allCached),
	}
	childExpandSpan := trace.start("cache.child_expand", childCounts)
	if err := prepareRemoteGoTestResumeLookup(
		ctx,
		cachePrefix,
		now,
		input,
		allCached,
		&lookup,
	); err != nil {
		childCounts.workloads = len(lookup.workerWorkloads)
		trace.finish(childExpandSpan, err, childCounts)
		return remoteWorkloadCacheSelection{}, err
	}
	childCounts.workloads = len(lookup.resume.entries)
	trace.finish(childExpandSpan, nil, childCounts)
	childLookupSpan := trace.start("cache.child_lookup", childCounts)
	if err := lookupExactPassedGoTests(ctx, store, cachePrefix, now, input, lookup, allCached); err != nil {
		childCounts.cacheHits = len(allCached)
		childCounts.cacheMisses = len(lookup.workerWorkloads) - len(allCached)
		trace.finish(childLookupSpan, err, childCounts)
		return remoteWorkloadCacheSelection{}, err
	}
	childCounts.cacheHits = len(allCached)
	childCounts.cacheMisses = len(lookup.workerWorkloads) - len(allCached)
	trace.finish(childLookupSpan, nil, childCounts)
	projectionCounts := remoteCIPhaseCounts{
		workloads:   len(lookup.workerWorkloads),
		cacheHits:   len(allCached),
		cacheMisses: len(lookup.workerWorkloads) - len(allCached),
	}
	cacheProjectionSpan := trace.start("cache.project", projectionCounts)
	if err := enforceRemoteCalibrationEvidence(lookup.workerWorkloads, allCached, &lookup.resume, input); err != nil {
		trace.finish(cacheProjectionSpan, err, projectionCounts)
		return remoteWorkloadCacheSelection{}, err
	}
	packageCached, testCached := splitRemoteGoTestCacheHits(lookup.workerWorkloads, allCached)
	selection, err := projectRemoteGoTestCacheSelection(
		catalog, packageCached, testCached, lookup.resume, lookup.inputDigests, input, cachePrefix,
	)
	trace.finish(cacheProjectionSpan, err, remoteCIPhaseCounts{
		workloads:   len(lookup.workerWorkloads),
		cacheHits:   len(selection.reused),
		cacheMisses: len(selection.workloads) - len(selection.reused),
	})
	return selection, err
}

func prepareRemoteWorkloadCacheLookup(
	ctx context.Context,
	cachePrefix string,
	now func() time.Time,
	input RunInput,
	catalog gate.WorkloadCatalog,
) (remoteWorkloadCacheLookup, error) {
	workerWorkloads := remoteShardableWorkloads(catalog)
	snapshot, err := loadRemoteGitTreeSnapshot(ctx, input.RepositoryRoot, input.Tree)
	if err != nil {
		return remoteWorkloadCacheLookup{}, err
	}
	inputDigests, err := snapshot.remoteWorkloadInputDigests(ctx, workerWorkloads)
	if err != nil {
		return remoteWorkloadCacheLookup{}, err
	}
	cacheEntries, err := remoteWorkloadCacheEntries(cachePrefix, workerWorkloads, inputDigests, input)
	if err != nil {
		return remoteWorkloadCacheLookup{}, err
	}
	if err := recordRemoteWorkloadFingerprints(input.LedgerStore, cacheEntries, input.Tree, now().UTC()); err != nil {
		return remoteWorkloadCacheLookup{}, err
	}
	packageLegacyEntries, err := remoteLegacyWorkloadCacheEntries(cachePrefix, cacheEntries, input)
	if err != nil {
		return remoteWorkloadCacheLookup{}, err
	}
	return remoteWorkloadCacheLookup{
		snapshot: snapshot, workerWorkloads: workerWorkloads, inputDigests: inputDigests,
		cacheEntries: cacheEntries, packageLegacyEntries: packageLegacyEntries,
	}, nil
}

// prepareRemoteGoTestResumeLookup 只为父目标缓存未命中的 Go 包展开逐测试身份。
func prepareRemoteGoTestResumeLookup(
	ctx context.Context,
	cachePrefix string,
	now func() time.Time,
	input RunInput,
	cached map[string]gate.PlanGateExecution,
	lookup *remoteWorkloadCacheLookup,
) error {
	misses := remoteUncachedWorkloads(lookup.workerWorkloads, cached)
	if len(misses) == 0 {
		return nil
	}
	inventories, err := lookup.snapshot.remoteGoTestInventories(ctx, misses, input.Platform)
	if err != nil {
		return err
	}
	childDigests, err := lookup.snapshot.remoteExactGoTestInputDigests(ctx, misses)
	if err != nil {
		return err
	}
	resumeDigests := make(map[string]string, len(misses)+len(childDigests))
	for _, workload := range misses {
		digest, ok := lookup.inputDigests[workload.ID]
		if !ok || digest == "" {
			return fmt.Errorf("cache-miss workload %q has no parent input digest", workload.ID)
		}
		resumeDigests[workload.ID] = digest
	}
	maps.Copy(resumeDigests, childDigests)
	resume, err := buildRemoteGoTestResumeSet(
		misses,
		inventories,
		resumeDigests,
		input,
		cachePrefix,
	)
	if err != nil {
		return err
	}
	if err := recordRemoteWorkloadFingerprints(
		input.LedgerStore,
		resume.entries,
		input.Tree,
		now().UTC(),
	); err != nil {
		return err
	}
	lookup.resume = resume
	return nil
}

func remoteUncachedWorkloads(
	workloads []gate.Workload,
	cached map[string]gate.PlanGateExecution,
) []gate.Workload {
	misses := make([]gate.Workload, 0, len(workloads))
	for _, workload := range workloads {
		if _, ok := cached[workload.ID]; !ok {
			misses = append(misses, workload)
		}
	}
	return misses
}

func lookupExactPassedWorkloads(
	ctx context.Context,
	store ObjectStore,
	now func() time.Time,
	input RunInput,
	lookup remoteWorkloadCacheLookup,
) (map[string]gate.PlanGateExecution, error) {
	if input.LedgerStore == nil {
		return loadPassedWorkloadCacheWithSQLite(
			ctx, store, nil, now, lookup.cacheEntries, lookup.packageLegacyEntries, input.ForceRerun,
		)
	}
	return lookupPassedWorkloadCacheProofsWithSQLite(
		input.LedgerStore, now, lookup.cacheEntries, input.ForceRerun,
	)
}

func promoteCompatiblePassedWorkloads(
	ctx context.Context,
	store ObjectStore,
	now func() time.Time,
	input RunInput,
	lookup remoteWorkloadCacheLookup,
	allCached map[string]gate.PlanGateExecution,
) error {
	compatibleCached, err := promoteCompatiblePassedWorkloadCache(
		ctx, store, input.LedgerStore, now, input.RepositoryRoot, lookup.workerWorkloads,
		remoteWorkloadCacheMissEntries(lookup.cacheEntries, allCached), input.ForceRerun,
	)
	if err != nil {
		return err
	}
	maps.Copy(allCached, compatibleCached)
	return nil
}

func loadUnknownPassedWorkloadsFromOSS(
	ctx context.Context,
	store ObjectStore,
	now func() time.Time,
	input RunInput,
	lookup remoteWorkloadCacheLookup,
	allCached map[string]gate.PlanGateExecution,
) error {
	if input.LedgerStore == nil {
		return nil
	}
	objectCached, err := loadPassedWorkloadCacheWithSQLite(
		ctx, store, input.LedgerStore, now,
		remoteWorkloadCacheMissEntries(lookup.cacheEntries, allCached),
		remoteWorkloadCacheMissEntries(lookup.packageLegacyEntries, allCached),
		input.ForceRerun,
	)
	if err != nil {
		return err
	}
	maps.Copy(allCached, objectCached)
	return nil
}

func lookupExactPassedGoTests(
	ctx context.Context,
	store ObjectStore,
	cachePrefix string,
	now func() time.Time,
	input RunInput,
	lookup remoteWorkloadCacheLookup,
	allCached map[string]gate.PlanGateExecution,
) error {
	testEntries, err := remoteRequiredGoTestCacheEntries(lookup.resume.entries, allCached)
	if err != nil {
		return err
	}
	compatibleCached, err := promoteCompatiblePassedWorkloadCache(
		ctx, store, input.LedgerStore, now, input.RepositoryRoot, lookup.workerWorkloads,
		testEntries, input.ForceRerun,
	)
	if err != nil {
		return err
	}
	maps.Copy(allCached, compatibleCached)
	testEntries, err = remoteRequiredGoTestCacheEntries(lookup.resume.entries, allCached)
	if err != nil {
		return err
	}
	testLegacyEntries, err := remoteLegacyWorkloadCacheEntries(cachePrefix, testEntries, input)
	if err != nil {
		return err
	}
	testCached, err := loadPassedWorkloadCacheWithSQLite(
		ctx, store, input.LedgerStore, now, testEntries, testLegacyEntries, input.ForceRerun,
	)
	if err != nil {
		return err
	}
	maps.Copy(allCached, testCached)
	return nil
}
