// Package heterocinode 提供远端节点 probe 程序的严格、非权威协议核心。
package heterocinode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

const ConfigSchemaVersion = "heteroci-node-probe-config/v1"

// ErrFactsUnavailable 表示节点当前无法取得完整资格或容量事实，可由 transport 投影为 offline。
var (
	ErrFactsUnavailable = errors.New("node probe facts are unavailable")
	ErrFactsInvalid     = errors.New("node probe facts are invalid")
	ErrResponseInvalid  = errors.New("node probe response is invalid")
)

// Config 将一个远端 probe 进程固定绑定到唯一 manifest node。
type Config struct {
	SchemaVersion string              `json:"schema_version"`
	Node          heteroci.NodeConfig `json:"node"`
}

// Facts 是平台 provider 在同一时刻采集的 boot、能力和容量事实。
type Facts struct {
	BootID       string
	Capabilities []heteroci.ActiveCapabilitySnapshot
	Total        heteroci.ResourceCapacity
	Available    heteroci.ResourceCapacity
}

// FactsProvider 采集当前节点的实际平台事实，不得从 manifest 回显容量。
type FactsProvider interface {
	Collect(context.Context, Config, heteroci.ProbeRequest) (Facts, error)
}

// Probe 校验 request 与本机 config 身份后采集事实并输出 canonical response bytes。
func Probe(ctx context.Context, config Config, requestBytes []byte, provider FactsProvider, now time.Time) ([]byte, error) {
	request, err := prepareProbe(ctx, config, requestBytes, provider)
	if err != nil {
		return nil, err
	}
	if now.UnixMilli() <= 0 {
		return nil, errors.New("node probe clock is invalid")
	}
	facts, err := provider.Collect(ctx, config, request)
	if err != nil {
		return nil, fmt.Errorf("%w: collect platform facts", ErrFactsUnavailable)
	}
	if err := validateFacts(config, facts); err != nil {
		return nil, fmt.Errorf("%w: validate platform facts", ErrFactsInvalid)
	}
	return encodeResponse(buildResponse(config, request, facts))
}

// prepareProbe 在采集平台事实前锁定依赖、context、config 和 request identity。
func prepareProbe(ctx context.Context, config Config, requestBytes []byte, provider FactsProvider) (heteroci.ProbeRequest, error) {
	if ctx == nil || provider == nil {
		return heteroci.ProbeRequest{}, errors.New("node probe dependencies are invalid")
	}
	if err := ctx.Err(); err != nil {
		return heteroci.ProbeRequest{}, err
	}
	if err := config.Validate(); err != nil {
		return heteroci.ProbeRequest{}, err
	}
	request, err := heteroci.DecodeProbeRequest(requestBytes)
	if err != nil {
		return heteroci.ProbeRequest{}, errors.New("node probe request is invalid")
	}
	if request.NodeID != config.Node.NodeID || request.ProbeIdentity != config.Node.ProbeIdentity {
		return heteroci.ProbeRequest{}, errors.New("node probe request identity drifted")
	}
	return request, nil
}

func encodeResponse(response heteroci.ProbeResponse) ([]byte, error) {
	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, ErrResponseInvalid
	}
	if _, err := heteroci.DecodeProbeResponse(bytes.NewReader(encoded)); err != nil {
		return nil, ErrResponseInvalid
	}
	return encoded, nil
}

// Validate 校验 probe config 的 schema 与单节点 manifest 语义。
func (config Config) Validate() error {
	if config.SchemaVersion != ConfigSchemaVersion {
		return errors.New("node probe config schema_version is invalid")
	}
	manifest := heteroci.Manifest{
		SchemaVersion: heteroci.ManifestSchemaVersion, Authority: heteroci.AuthorityLocalNonAuthoritative,
		CloudOverflow: heteroci.CloudOverflowConfig{}, Nodes: []heteroci.NodeConfig{config.Node},
	}
	if err := manifest.Validate(); err != nil {
		return fmt.Errorf("node probe config node is invalid: %w", err)
	}
	return nil
}

// validateFacts 校验 provider 事实的身份覆盖和 total/available 容量关系。
func validateFacts(config Config, facts Facts) error {
	if facts.BootID == "" || len(facts.Capabilities) != len(config.Node.Capabilities) {
		return errors.New("node probe facts identity is incomplete")
	}
	if err := validateCapacity(facts.Total, "total"); err != nil {
		return err
	}
	if err := validateCapacity(facts.Available, "available"); err != nil {
		return err
	}
	if facts.Available.CPUMilli > facts.Total.CPUMilli || facts.Available.MemoryBytes > facts.Total.MemoryBytes || facts.Available.MaxConcurrency > facts.Total.MaxConcurrency {
		return errors.New("node probe available capacity exceeds total")
	}
	return nil
}

func validateCapacity(capacity heteroci.ResourceCapacity, label string) error {
	if capacity.CPUMilli <= 0 || capacity.MemoryBytes <= 0 || capacity.MaxConcurrency <= 0 {
		return fmt.Errorf("node probe %s capacity must be positive", label)
	}
	return nil
}

func buildResponse(config Config, request heteroci.ProbeRequest, facts Facts) heteroci.ProbeResponse {
	return heteroci.ProbeResponse{
		SchemaVersion: heteroci.ProbeResponseSchemaVersion, OwnerEpoch: request.OwnerEpoch,
		CapacityBasis: heteroci.SnapshotCapacityBasisExternalAvailableExcludesRoutingBookReservations,
		NodeID:        config.Node.NodeID, Endpoint: config.Node.Endpoint, Platform: config.Node.Platform,
		ProbeIdentity: config.Node.ProbeIdentity, ProbeBootID: facts.BootID,
		ActiveCapabilities: slices.Clone(facts.Capabilities), Status: heteroci.NodeStatusLive,
		HeartbeatUnixMilli: request.CapturedAtUnixMilli, Total: facts.Total, Available: facts.Available,
	}
}
