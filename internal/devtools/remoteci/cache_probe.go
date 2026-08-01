package remoteci

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// WorkloadCacheProbeResult is the read-only cache decision made before an execution backend is selected.
type WorkloadCacheProbeResult struct {
	Catalog            gate.WorkloadCatalog
	ReusedWorkloads    []gate.GateID
	CacheMissWorkloads []gate.Workload
}

// WorkloadCacheProbe reads authoritative remote PASS markers without requiring an ECI runtime.
type WorkloadCacheProbe struct {
	prefix string
	store  ObjectStore
	now    func() time.Time
}

// NewWorkloadCacheProbe 构造本地与远程调用方共享的 PASS 标记读取器。
func NewWorkloadCacheProbe(prefix string, store ObjectStore) (*WorkloadCacheProbe, error) {
	if store == nil {
		return nil, errors.New("remote workload cache object store is required")
	}
	if !validObjectPrefix(prefix) {
		return nil, errors.New("remote workload cache prefix is invalid")
	}
	return &WorkloadCacheProbe{prefix: prefix, store: store, now: time.Now}, nil
}

// Probe 计算精确 Git tree 指纹，并只返回仍需执行的 workload。
func (probe *WorkloadCacheProbe) Probe(ctx context.Context, input RunInput) (WorkloadCacheProbeResult, error) {
	if ctx == nil {
		return WorkloadCacheProbeResult{}, errors.New("remote workload cache probe context is required")
	}
	if probe == nil || probe.store == nil || probe.now == nil {
		return WorkloadCacheProbeResult{}, errors.New("remote workload cache probe is not configured")
	}
	_, catalog, _, err := buildRemotePlan(input)
	if err != nil {
		return WorkloadCacheProbeResult{}, err
	}
	selection, err := lookupPassedWorkloads(
		ctx,
		probe.store,
		probe.prefix,
		probe.now,
		input,
		catalog,
		nil,
	)
	if err != nil {
		return WorkloadCacheProbeResult{}, err
	}
	reused := make(map[gate.GateID]struct{}, len(selection.reused))
	for _, workloadID := range selection.reused {
		reused[workloadID] = struct{}{}
	}
	misses := make([]gate.Workload, 0, len(selection.workloads)-len(reused))
	for _, workload := range selection.workloads {
		if _, ok := reused[gate.GateID(workload.ID)]; !ok {
			misses = append(misses, workload)
		}
	}
	return WorkloadCacheProbeResult{
		Catalog: gate.WorkloadCatalog{
			Version:       selection.catalog.Version,
			Authoritative: selection.catalog.Authoritative,
			Workloads:     slices.Clone(selection.catalog.Workloads),
		},
		ReusedWorkloads:    slices.Clone(selection.reused),
		CacheMissWorkloads: misses,
	}, nil
}
