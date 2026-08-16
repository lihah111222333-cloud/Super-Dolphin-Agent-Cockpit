package gate

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// WorkloadPlanningTierResolution binds one tier's resolution to reserve evidence.
type WorkloadPlanningTierResolution struct {
	ResourceTier cicontract.WorkloadResourceTier                    `json:"resource_tier"`
	Resolution   cicontract.WorkloadPlanningReceiptTierResolution   `json:"resolution"`
	Measurement  *cicontract.WorkloadResourceTierReserveMeasurement `json:"measurement,omitempty"`
}

// WorkloadPlanningReceipt is the strict planned/not_applicable receipt union.
type WorkloadPlanningReceipt struct {
	Status        cicontract.WorkloadPlanningReceiptStatus `json:"status"`
	Planned       *WorkloadPlanningReceiptPlanned          `json:"planned,omitempty"`
	NotApplicable *WorkloadPlanningReceiptNotApplicable    `json:"not_applicable,omitempty"`
}

// WorkloadPlanningReceiptPlanned carries canonical planning inputs and results.
type WorkloadPlanningReceiptPlanned struct {
	ReceiptSchemaVersion    uint32                           `json:"receipt_schema_version"`
	PlanSchemaVersion       uint32                           `json:"plan_schema_version"`
	AlgorithmID             string                           `json:"algorithm_id"`
	ObjectiveDigest         string                           `json:"objective_digest"`
	PlanningPolicyDigest    string                           `json:"planning_policy_digest"`
	EstimationPolicyDigest  string                           `json:"estimation_policy_digest"`
	GatePlanDigest          string                           `json:"gate_plan_digest"`
	CatalogDigest           string                           `json:"catalog_digest"`
	LedgerGeneration        uint64                           `json:"ledger_generation"`
	ExecutionWorkloadDigest string                           `json:"execution_workload_digest"`
	PlanDigest              string                           `json:"plan_digest"`
	TierResolutions         []WorkloadPlanningTierResolution `json:"tier_resolutions"`
	PackingEvidence         []WorkloadPackingEvidence        `json:"packing_evidence"`
	MappingRoot             string                           `json:"mapping_root"`
	PlannedWorkloadCount    int                              `json:"planned_workload_count"`
	PlannedShardCount       int                              `json:"planned_shard_count"`
}

// WorkloadPlanningReceiptNotApplicable carries a pre-planning terminal reason.
type WorkloadPlanningReceiptNotApplicable struct {
	Reason           cicontract.WorkloadPlanningReceiptReason `json:"reason"`
	GatePlanDigest   string                                   `json:"gate_plan_digest"`
	CatalogDigest    string                                   `json:"catalog_digest"`
	LedgerGeneration uint64                                   `json:"ledger_generation"`
}

// WorkloadPlanningMappingEntry binds one canonical workload to one shard.
type WorkloadPlanningMappingEntry struct {
	WorkloadID    string `json:"workload_id"`
	ShardIdentity string `json:"shard_identity"`
}

// Validate rejects malformed or semantically mixed receipt payloads.
func (receipt WorkloadPlanningReceipt) Validate() error {
	if err := cicontract.ValidateWorkloadPlanningReceiptStatus(receipt.Status); err != nil {
		return err
	}
	if (receipt.Planned == nil) == (receipt.NotApplicable == nil) {
		return errors.New("workload planning receipt requires exactly one payload")
	}
	if receipt.Status == cicontract.WorkloadPlanningReceiptStatusPlanned {
		if receipt.Planned == nil {
			return errors.New("planned workload planning receipt payload is missing")
		}
		if receipt.NotApplicable != nil {
			return errors.New("planned workload planning receipt has not_applicable payload")
		}
		return receipt.Planned.validate()
	}
	if receipt.NotApplicable == nil {
		return errors.New("not_applicable workload planning receipt payload is missing")
	}
	return receipt.NotApplicable.validate()
}
func (payload WorkloadPlanningReceiptPlanned) validate() error {
	if err := payload.validateIdentity(); err != nil {
		return err
	}
	if err := payload.validateDigests(); err != nil {
		return err
	}
	if payload.LedgerGeneration == 0 || payload.PlannedWorkloadCount < 0 || payload.PlannedShardCount < 0 {
		return errors.New("workload planning receipt counts or generation are invalid")
	}
	calibration, err := payload.validateTiers()
	if err != nil {
		return err
	}
	return payload.validateCounts(calibration)
}
func (payload WorkloadPlanningReceiptPlanned) validateIdentity() error {
	if err := cicontract.ValidateWorkloadPlanningReceiptSchema(payload.ReceiptSchemaVersion); err != nil {
		return err
	}
	if payload.PlanSchemaVersion != cicontract.WorkloadExecutionPlanSchemaVersion || payload.AlgorithmID != cicontract.WorkloadPlanningAlgorithmID || payload.ObjectiveDigest != cicontract.WorkloadPlanningObjectiveDigest() {
		return errors.New("workload planning receipt plan identity is not canonical")
	}
	policyDigest, err := cicontract.WorkloadPlanningPolicyDigest()
	if err != nil {
		return fmt.Errorf("compute planning policy digest: %w", err)
	}
	if payload.PlanningPolicyDigest != policyDigest {
		return errors.New("workload planning receipt planning policy digest is not canonical")
	}
	return nil
}
func (payload WorkloadPlanningReceiptPlanned) validateDigests() error {
	for name, digest := range map[string]string{
		"estimation_policy_digest":  payload.EstimationPolicyDigest,
		"gate_plan_digest":          payload.GatePlanDigest,
		"catalog_digest":            payload.CatalogDigest,
		"execution_workload_digest": payload.ExecutionWorkloadDigest,
		"plan_digest":               payload.PlanDigest,
		"mapping_root":              payload.MappingRoot,
	} {
		if err := cicontract.ValidateWorkloadPlanningReceiptDigest(digest); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}
func (payload WorkloadPlanningReceiptPlanned) validateTiers() (bool, error) {
	if len(payload.TierResolutions) != int(cicontract.WorkloadResourceTierSlow) || len(payload.PackingEvidence) != len(payload.TierResolutions) {
		return false, errors.New("workload planning receipt requires exactly three tier resolutions and evidence entries")
	}
	calibration := true
	seenNotApplicable := false
	for index := range payload.TierResolutions {
		resolution := payload.TierResolutions[index]
		evidence := payload.PackingEvidence[index]
		wantTier := cicontract.WorkloadResourceTier(index + 1)
		if resolution.ResourceTier != wantTier || evidence.ResourceTier != wantTier {
			return false, errors.New("workload planning receipt tier order is not canonical")
		}
		if resolution.Resolution != cicontract.WorkloadPlanningReceiptTierResolutionNotApplicable {
			if seenNotApplicable {
				return false, errors.New("normal workload planning receipt must not contain not_applicable tier resolution")
			}
			calibration = false
		} else {
			seenNotApplicable = true
		}
		if err := validateReceiptTierResolution(resolution); err != nil {
			return false, err
		}
		if err := validateReceiptPackingEvidence(evidence, resolution.Resolution, index); err != nil {
			return false, err
		}
	}
	return calibration, nil
}
func (payload WorkloadPlanningReceiptPlanned) validateCounts(calibration bool) error {
	if calibration {
		if payload.PlannedWorkloadCount == 0 && payload.PlannedShardCount != 0 {
			return errors.New("calibration workload planning receipt has inconsistent counts")
		}
		return nil
	}
	if payload.PlannedWorkloadCount == 0 || payload.PlannedShardCount == 0 || payload.PlannedShardCount > payload.PlannedWorkloadCount {
		return errors.New("normal workload planning receipt counts are invalid")
	}
	shardCount := 0
	for _, evidence := range payload.PackingEvidence {
		shardCount += evidence.PlannedShards
	}
	if shardCount != payload.PlannedShardCount {
		return errors.New("workload planning receipt planned shard count does not match evidence")
	}
	return nil
}
func (payload WorkloadPlanningReceiptNotApplicable) validate() error {
	if err := cicontract.ValidateWorkloadPlanningReceiptReason(payload.Reason); err != nil {
		return err
	}
	if err := cicontract.ValidateWorkloadPlanningReceiptDigest(payload.GatePlanDigest); err != nil {
		return fmt.Errorf("gate_plan_digest: %w", err)
	}
	if err := cicontract.ValidateWorkloadPlanningReceiptDigest(payload.CatalogDigest); err != nil {
		return fmt.Errorf("catalog_digest: %w", err)
	}
	if payload.LedgerGeneration == 0 {
		return errors.New("not_applicable workload planning receipt generation must be positive")
	}
	return nil
}
func validateReceiptTierResolution(resolution WorkloadPlanningTierResolution) error {
	if err := cicontract.ValidateWorkloadPlanningReceiptTierResolution(resolution.Resolution); err != nil {
		return err
	}
	switch resolution.Resolution {
	case cicontract.WorkloadPlanningReceiptTierResolutionMeasured:
		if resolution.Measurement == nil || resolution.Measurement.P95MS < 0 || resolution.Measurement.SampleCount <= 0 {
			return errors.New("measured workload planning tier resolution is incomplete")
		}
		if resolution.Measurement.P95MS >= int64(cicontract.ShardTargetDuration/time.Millisecond) {
			return errors.New("measured workload planning tier reserve leaves no planning budget")
		}
		if err := cicontract.ValidateWorkloadPlanningReceiptDigest(resolution.Measurement.ProvenanceDigest); err != nil {
			return fmt.Errorf("measured tier provenance: %w", err)
		}
	case cicontract.WorkloadPlanningReceiptTierResolutionBootstrap, cicontract.WorkloadPlanningReceiptTierResolutionNotApplicable:
		if resolution.Measurement != nil {
			return errors.New("bootstrap or not_applicable tier resolution must not carry measurement")
		}
	}
	return nil
}
func validateReceiptPackingEvidence(evidence WorkloadPackingEvidence, resolution cicontract.WorkloadPlanningReceiptTierResolution, index int) error {
	if resolution == cicontract.WorkloadPlanningReceiptTierResolutionNotApplicable {
		if evidence != (WorkloadPackingEvidence{ResourceTier: evidence.ResourceTier, Resolution: cicontract.WorkloadResourceTierReserveResolution(cicontract.WorkloadPlanningReceiptTierResolutionNotApplicable), SolverMode: cicontract.WorkloadPlanningReceiptNotApplicableEvidence}) {
			return fmt.Errorf("packing evidence %d is not canonical not_applicable evidence", index)
		}
		return nil
	}
	if evidence.Resolution != cicontract.WorkloadResourceTierReserveResolution(resolution) {
		return fmt.Errorf("packing evidence %d resolution does not match tier resolution", index)
	}
	if err := validatePackingEvidenceCounts(index, evidence); err != nil {
		return err
	}
	return validatePackingEvidenceMode(index, evidence)
}

// EncodeCanonicalWorkloadPlanningReceipt emits canonical JSON after validation.
func EncodeCanonicalWorkloadPlanningReceipt(receipt WorkloadPlanningReceipt) ([]byte, error) {
	if err := receipt.Validate(); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return nil, fmt.Errorf("encode workload planning receipt: %w", err)
	}
	return encoded, nil
}

// DecodeCanonicalWorkloadPlanningReceipt accepts only canonical validated JSON.
func DecodeCanonicalWorkloadPlanningReceipt(data []byte) (WorkloadPlanningReceipt, error) {
	if len(data) == 0 {
		return WorkloadPlanningReceipt{}, errors.New("workload planning receipt is empty")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var receipt WorkloadPlanningReceipt
	if err := decoder.Decode(&receipt); err != nil {
		return WorkloadPlanningReceipt{}, fmt.Errorf("decode workload planning receipt: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return WorkloadPlanningReceipt{}, errors.New("workload planning receipt contains multiple JSON values")
		}
		return WorkloadPlanningReceipt{}, fmt.Errorf("decode workload planning receipt trailer: %w", err)
	}
	if err := receipt.Validate(); err != nil {
		return WorkloadPlanningReceipt{}, err
	}
	canonical, err := EncodeCanonicalWorkloadPlanningReceipt(receipt)
	if err != nil {
		return WorkloadPlanningReceipt{}, err
	}
	if !bytes.Equal(data, canonical) {
		return WorkloadPlanningReceipt{}, errors.New("workload planning receipt bytes are not canonical")
	}
	return receipt, nil
}

// WorkloadPlanningReceiptDigest returns a domain-separated canonical digest.
func WorkloadPlanningReceiptDigest(receipt WorkloadPlanningReceipt) (string, error) {
	encoded, err := EncodeCanonicalWorkloadPlanningReceipt(receipt)
	if err != nil {
		return "", err
	}
	material, err := json.Marshal(struct {
		Domain  string          `json:"domain"`
		Receipt json.RawMessage `json:"receipt"`
	}{Domain: cicontract.WorkloadPlanningReceiptDomain, Receipt: encoded})
	if err != nil {
		return "", fmt.Errorf("encode workload planning receipt digest material: %w", err)
	}
	digest := sha256.Sum256(material)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// ComputeWorkloadPlanningMappingRoot returns the sorted duplicate-last Merkle root.
func ComputeWorkloadPlanningMappingRoot(entries []WorkloadPlanningMappingEntry) (string, error) {
	canonical, err := canonicalWorkloadMappingEntries(entries)
	if err != nil {
		return "", err
	}
	leaves, err := workloadMappingLeaves(canonical)
	if err != nil {
		return "", err
	}
	root := workloadMappingMerkleRoot(leaves, len(canonical))
	return "sha256:" + hex.EncodeToString(root[:]), nil
}
func canonicalWorkloadMappingEntries(entries []WorkloadPlanningMappingEntry) ([]WorkloadPlanningMappingEntry, error) {
	if len(entries) == 0 {
		return nil, errors.New("workload planning mapping is empty")
	}
	canonical := append([]WorkloadPlanningMappingEntry(nil), entries...)
	sort.SliceStable(canonical, func(left, right int) bool { return workloadMappingEntryLess(canonical[left], canonical[right]) })
	return canonical, nil
}
func workloadMappingEntryLess(left, right WorkloadPlanningMappingEntry) bool {
	if left.WorkloadID != right.WorkloadID {
		return left.WorkloadID < right.WorkloadID
	}
	return left.ShardIdentity < right.ShardIdentity
}
func workloadMappingLeaves(entries []WorkloadPlanningMappingEntry) ([][sha256.Size]byte, error) {
	seen := make(map[string]struct{}, len(entries))
	leaves := make([][sha256.Size]byte, len(entries))
	for index, entry := range entries {
		if err := validateCanonicalWorkloadMappingID(entry.WorkloadID); err != nil {
			return nil, fmt.Errorf("workload planning mapping entry %d workload_id: %w", index, err)
		}
		if err := cicontract.ValidateWorkloadPlanningReceiptDigest(entry.ShardIdentity); err != nil {
			return nil, fmt.Errorf("workload planning mapping entry %d shard_identity: %w", index, err)
		}
		if _, duplicate := seen[entry.WorkloadID]; duplicate {
			return nil, fmt.Errorf("workload planning mapping workload %q is duplicated", entry.WorkloadID)
		}
		seen[entry.WorkloadID] = struct{}{}
		material := append([]byte{'l'}, []byte(cicontract.WorkloadPlanningMappingDomain)...)
		material = appendLengthPrefixed(material, entry.WorkloadID)
		material = appendLengthPrefixed(material, entry.ShardIdentity)
		leaves[index] = sha256.Sum256(material)
	}
	return leaves, nil
}
func workloadMappingMerkleRoot(leaves [][sha256.Size]byte, leafCount int) [sha256.Size]byte {
	for len(leaves) > 1 {
		next := make([][sha256.Size]byte, 0, (len(leaves)+1)/2)
		for index := 0; index < len(leaves); index += 2 {
			right := leaves[index]
			if index+1 < len(leaves) {
				right = leaves[index+1]
			}
			material := append([]byte{'n'}, []byte(cicontract.WorkloadPlanningMappingDomain)...)
			material = append(material, leaves[index][:]...)
			material = append(material, right[:]...)
			next = append(next, sha256.Sum256(material))
		}
		leaves = next
	}
	rootMaterial := append([]byte{'r'}, []byte(cicontract.WorkloadPlanningMappingDomain)...)
	var count [8]byte
	binary.BigEndian.PutUint64(count[:], uint64(leafCount))
	rootMaterial = append(rootMaterial, count[:]...)
	rootMaterial = append(rootMaterial, leaves[0][:]...)
	return sha256.Sum256(rootMaterial)
}
func appendLengthPrefixed(dst []byte, value string) []byte {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len([]byte(value))))
	dst = append(dst, length[:]...)
	return append(dst, []byte(value)...)
}
func validateCanonicalWorkloadMappingID(id string) error {
	if id == "" || strings.TrimSpace(id) != id || !utf8.ValidString(id) {
		return errors.New("workload ID is empty, padded, or invalid UTF-8")
	}
	parent, kind, target, targeted, err := ParseWorkloadID(id)
	if err != nil {
		return fmt.Errorf("workload ID is not canonical: %w", err)
	}
	if targeted {
		canonical, err := targetWorkloadID(parent, string(kind), target)
		if err != nil || canonical != id {
			return errors.New("target workload ID is not canonical")
		}
		return nil
	}
	for _, spec := range GateRegistry() {
		if spec.ID == parent {
			return nil
		}
	}
	return fmt.Errorf("workload ID %q is not a registered canonical gate", id)
}
