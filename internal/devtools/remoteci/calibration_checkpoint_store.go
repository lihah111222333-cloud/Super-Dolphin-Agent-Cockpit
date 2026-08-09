package remoteci

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
)

const (
	calibrationCheckpointSchemaVersion uint32 = 3
)

type calibrationCheckpointDocument struct {
	SchemaVersion    uint32                              `json:"schema_version"`
	Identity         string                              `json:"identity"`
	AgentTokenDigest string                              `json:"agent_token_digest"`
	Scenarios        map[string]calibrationScenarioState `json:"scenarios"`
}

type calibrationScenarioState struct {
	Started   bool                         `json:"started"`
	Completed bool                         `json:"completed"`
	Input     *calibrationCheckpointInput  `json:"input,omitempty"`
	Result    *calibrationCheckpointResult `json:"result,omitempty"`
}

type calibrationCheckpointInput struct {
	AgentTokenDigest             string                         `json:"agent_token_digest"`
	AcceptedGeneration           uint64                         `json:"accepted_generation"`
	ImageCacheSnapshotID         string                         `json:"image_cache_snapshot_id"`
	Tree                         string                         `json:"tree"`
	Source                       gatecontract.SourceSpec        `json:"source"`
	Profile                      gatecontract.Profile           `json:"profile"`
	Entrypoint                   gatecontract.CIEntrypointID    `json:"entrypoint"`
	Platform                     string                         `json:"platform"`
	ToolchainDigest              string                         `json:"toolchain_digest"`
	CandidateGateSourceSHA256    string                         `json:"candidate_gate_source_sha256"`
	CandidateGateToolchainSHA256 string                         `json:"candidate_gate_toolchain_sha256"`
	Inventory                    gatecontract.WorkloadInventory `json:"inventory"`
	Calibration                  bool                           `json:"calibration"`
	Force                        bool                           `json:"force"`
	RunnerIdentityDigest         string                         `json:"runner_identity_digest"`
	RunnerImage                  string                         `json:"runner_image"`
	CalibrationResource          shardresource.Class            `json:"calibration_resource"`
}

// Validate 检查断点输入载荷是否已初始化。
func (input *calibrationCheckpointInput) Validate() error {
	if input == nil {
		return errors.New("calibration checkpoint input is required")
	}
	return nil
}

type calibrationCheckpointResult struct {
	AgentTokenDigest             string                      `json:"agent_token_digest"`
	AcceptedGeneration           uint64                      `json:"accepted_generation"`
	ImageCacheSnapshotID         string                      `json:"image_cache_snapshot_id"`
	JobID                        string                      `json:"job_id"`
	PlanDigest                   string                      `json:"plan_digest"`
	CatalogDigest                string                      `json:"catalog_digest"`
	SourceTreeSHA                string                      `json:"source_tree_sha"`
	CandidateGateSourceSHA256    string                      `json:"candidate_gate_source_sha256"`
	CandidateGateToolchainSHA256 string                      `json:"candidate_gate_toolchain_sha256"`
	Entrypoint                   gatecontract.CIEntrypointID `json:"entrypoint"`
	Profile                      gatecontract.Profile        `json:"profile"`
	Force                        bool                        `json:"force"`
	Status                       gatecontract.ResultStatus   `json:"status"`
	Authoritative                bool                        `json:"authoritative"`
	CleanupComplete              bool                        `json:"cleanup_complete"`
	CalibrationResourceClassID   string                      `json:"calibration_resource_class_id"`
	CalibrationResourceCPU       float64                     `json:"calibration_resource_cpu"`
	CalibrationResourceMemoryGiB float64                     `json:"calibration_resource_memory_gib"`
	CompletedAt                  time.Time                   `json:"completed_at"`
}

// Validate 检查断点结果载荷是否已初始化。
func (result *calibrationCheckpointResult) Validate() error {
	if result == nil {
		return errors.New("calibration checkpoint result is required")
	}
	return nil
}

// Validate 拒绝版本漂移、空身份和无法恢复的场景状态。
func (document *calibrationCheckpointDocument) Validate() error {
	if document.SchemaVersion != calibrationCheckpointSchemaVersion ||
		strings.TrimSpace(document.Identity) == "" || cicontract.ValidateAgentTokenDigest(document.AgentTokenDigest) != nil || document.Scenarios == nil {
		return errors.New("calibration checkpoint schema or identity is invalid")
	}
	return validateCalibrationCheckpointDocument(*document)
}

// CalibrationCheckpoint 将校准场景进度持久化在 duration ledger 的 SQLite 权威库中。
type CalibrationCheckpoint struct {
	store              *gatecontract.DurationLedgerStore
	identity           string
	acceptedGeneration uint64
	agentTokenDigest   string
}

// NewCalibrationCheckpoint 打开 duration ledger SQLite 权威库。
func NewCalibrationCheckpoint(store *gatecontract.DurationLedgerStore, identity string, acceptedGeneration uint64, agentTokenDigest string) (*CalibrationCheckpoint, error) {
	if store == nil || strings.TrimSpace(identity) == "" || acceptedGeneration == 0 || cicontract.ValidateAgentTokenDigest(agentTokenDigest) != nil {
		return nil, errors.New("calibration checkpoint duration ledger store, identity, accepted generation, and agent token digest are required")
	}
	checkpoint := &CalibrationCheckpoint{
		store:              store,
		identity:           identity,
		acceptedGeneration: acceptedGeneration,
		agentTokenDigest:   agentTokenDigest,
	}
	if err := checkpoint.ensure(); err != nil {
		return nil, err
	}
	return checkpoint, nil
}

func (checkpoint *CalibrationCheckpoint) ensure() error {
	record, found, err := checkpoint.store.LoadCalibrationCheckpoint(checkpoint.identity, checkpoint.agentTokenDigest)
	if err != nil {
		return err
	}
	if found {
		if record.AcceptedGeneration != checkpoint.acceptedGeneration {
			return errors.New("calibration checkpoint accepted generation does not match authority")
		}
		if record.SchemaVersion != calibrationCheckpointSchemaVersion {
			return errors.New("calibration checkpoint schema version is incompatible; retained checkpoint data must be discarded")
		}
	}
	return nil
}

// Completed 返回已经权威完成的场景输入与结果；读取或校验失败直接返回错误。
func (checkpoint *CalibrationCheckpoint) Completed(scenario string) (RunInput, RunResult, bool, error) {
	document, err := checkpoint.loadDocument()
	if err != nil {
		return RunInput{}, RunResult{}, false, err
	}
	state, ok := document.Scenarios[scenario]
	if !ok || !state.Completed || state.Input == nil || state.Result == nil {
		return RunInput{}, RunResult{}, false, nil
	}
	return state.Input.expand(), state.Result.expand(), true, nil
}

// Reopen 清除场景的完成载荷，但保留已开始状态供缓存恢复执行。
func (checkpoint *CalibrationCheckpoint) Reopen(scenario string) error {
	document, err := checkpoint.loadDocument()
	if err != nil {
		return err
	}
	state, ok := document.Scenarios[scenario]
	if !ok || !state.Completed {
		return nil
	}
	return checkpoint.compareAndSwapScenario(scenario, &state, calibrationScenarioState{Started: true})
}

// Observe 原子保存包含成功时长样本的执行结果。
func (checkpoint *CalibrationCheckpoint) Observe(scenario string, input RunInput, result RunResult, completed bool) error {
	if err := checkpoint.validateObservation(scenario, input, result); err != nil {
		return err
	}
	if completed {
		if err := validateCompletedCalibrationCheckpoint(*compactCalibrationCheckpointInput(input), *compactCalibrationCheckpointResult(result)); err != nil {
			return err
		}
	}
	document, err := checkpoint.loadDocument()
	if err != nil {
		return err
	}
	if len(result.DurationSamples) == 0 {
		return checkpoint.completeResumedScenario(document, scenario, input, result, completed)
	}
	return checkpoint.saveObservedScenario(document, scenario, input, result, completed)
}

// validateObservation 检查观测结果是否属于当前已接受的校准身份。
func (checkpoint *CalibrationCheckpoint) validateObservation(scenario string, input RunInput, result RunResult) error {
	if strings.TrimSpace(scenario) == "" {
		return errors.New("calibration checkpoint scenario is required")
	}
	if !input.Calibration {
		return errors.New("calibration checkpoint input is not a calibration run")
	}
	if input.AcceptedGeneration != checkpoint.acceptedGeneration || result.AcceptedGeneration != checkpoint.acceptedGeneration {
		return errors.New("calibration checkpoint execution accepted generation does not match authority")
	}
	return nil
}

// completeResumedScenario 将无时长样本的已开始场景原子标记为完成。
func (checkpoint *CalibrationCheckpoint) completeResumedScenario(document calibrationCheckpointDocument, scenario string, input RunInput, result RunResult, completed bool) error {
	state, ok := document.Scenarios[scenario]
	if !ok || !state.Started || !completed {
		return nil
	}
	previous := state
	state.Completed = true
	state.Input = compactCalibrationCheckpointInput(input)
	state.Result = compactCalibrationCheckpointResult(result)
	return checkpoint.compareAndSwapScenario(scenario, &previous, state)
}

// saveObservedScenario 原子保存包含时长样本的场景状态。
func (checkpoint *CalibrationCheckpoint) saveObservedScenario(document calibrationCheckpointDocument, scenario string, input RunInput, result RunResult, completed bool) error {
	state := calibrationScenarioState{Started: true, Completed: completed}
	if completed {
		state.Input = compactCalibrationCheckpointInput(input)
		state.Result = compactCalibrationCheckpointResult(result)
	}
	previous, exists := document.Scenarios[scenario]
	if !exists {
		return checkpoint.compareAndSwapScenario(scenario, nil, state)
	}
	return checkpoint.compareAndSwapScenario(scenario, &previous, state)
}

// loadDocument 从权威 duration ledger 重建并校验断点文档。
func (checkpoint *CalibrationCheckpoint) loadDocument() (calibrationCheckpointDocument, error) {
	document := calibrationCheckpointDocument{SchemaVersion: calibrationCheckpointSchemaVersion, Identity: checkpoint.identity, AgentTokenDigest: checkpoint.agentTokenDigest, Scenarios: make(map[string]calibrationScenarioState)}
	record, found, err := checkpoint.store.LoadCalibrationCheckpoint(checkpoint.identity, checkpoint.agentTokenDigest)
	if err != nil {
		return calibrationCheckpointDocument{}, err
	}
	if !found {
		return document, nil
	}
	if record.AcceptedGeneration != checkpoint.acceptedGeneration {
		return calibrationCheckpointDocument{}, errors.New("calibration checkpoint accepted generation does not match authority")
	}
	document.SchemaVersion = record.SchemaVersion
	if record.AgentTokenDigest != checkpoint.agentTokenDigest {
		return calibrationCheckpointDocument{}, errors.New("calibration checkpoint agent token digest does not match authority")
	}
	for _, persisted := range record.Scenarios {
		state, err := decodeCalibrationCheckpointState(boolToInt(persisted.Started), boolToInt(persisted.Completed), persisted.InputJSON, persisted.ResultJSON)
		if err != nil {
			return calibrationCheckpointDocument{}, fmt.Errorf("decode calibration checkpoint scenario %q: %w", persisted.Scenario, err)
		}
		document.Scenarios[persisted.Scenario] = state
	}
	if err := document.Validate(); err != nil {
		return calibrationCheckpointDocument{}, err
	}
	return document, nil
}

// decodeCalibrationCheckpointState 严格解码单个持久化场景状态。
func decodeCalibrationCheckpointState(started, completed int, inputJSON, resultJSON string) (calibrationScenarioState, error) {
	if started != 1 || (completed != 0 && completed != 1) {
		return calibrationScenarioState{}, errors.New("invalid persisted state")
	}
	state := calibrationScenarioState{Started: true, Completed: completed == 1}
	if !state.Completed {
		if inputJSON != "" || resultJSON != "" {
			return calibrationScenarioState{}, errors.New("incomplete state retained execution payload")
		}
		return state, nil
	}
	state.Input = &calibrationCheckpointInput{}
	state.Result = &calibrationCheckpointResult{}
	if err := gatecontract.DecodeStrictJSON([]byte(inputJSON), state.Input); err != nil {
		return calibrationScenarioState{}, fmt.Errorf("decode input: %w", err)
	}
	if err := gatecontract.DecodeStrictJSON([]byte(resultJSON), state.Result); err != nil {
		return calibrationScenarioState{}, fmt.Errorf("decode result: %w", err)
	}
	return state, nil
}

func (checkpoint *CalibrationCheckpoint) compareAndSwapScenario(scenario string, expected *calibrationScenarioState, next calibrationScenarioState) error {
	nextRecord, err := calibrationCheckpointScenarioRecord(scenario, next)
	if err != nil {
		return err
	}
	var expectedRecord *gatecontract.CalibrationCheckpointScenarioRecord
	if expected != nil {
		record, err := calibrationCheckpointScenarioRecord(scenario, *expected)
		if err != nil {
			return err
		}
		expectedRecord = &record
	}
	return checkpoint.store.CompareAndSwapCalibrationCheckpointScenario(checkpoint.identity, checkpoint.agentTokenDigest, calibrationCheckpointSchemaVersion, checkpoint.acceptedGeneration, expectedRecord, nextRecord)
}

func calibrationCheckpointScenarioRecord(scenario string, state calibrationScenarioState) (gatecontract.CalibrationCheckpointScenarioRecord, error) {
	inputJSON, resultJSON, err := encodeCalibrationCheckpointState(state)
	if err != nil {
		return gatecontract.CalibrationCheckpointScenarioRecord{}, err
	}
	return gatecontract.CalibrationCheckpointScenarioRecord{Scenario: scenario, Started: state.Started, Completed: state.Completed, InputJSON: inputJSON, ResultJSON: resultJSON}, nil
}

func encodeCalibrationCheckpointState(state calibrationScenarioState) (string, string, error) {
	if !state.Completed {
		return "", "", nil
	}
	inputJSON, err := json.Marshal(state.Input)
	if err != nil {
		return "", "", err
	}
	resultJSON, err := json.Marshal(state.Result)
	if err != nil {
		return "", "", err
	}
	return string(inputJSON), string(resultJSON), nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// Remove 删除已经接受的校准断点。
func (checkpoint *CalibrationCheckpoint) Remove() error {
	return checkpoint.store.DeleteCalibrationCheckpoint(checkpoint.identity, checkpoint.agentTokenDigest)
}

func compactCalibrationCheckpointInput(input RunInput) *calibrationCheckpointInput {
	return &calibrationCheckpointInput{AgentTokenDigest: input.AgentTokenDigest, AcceptedGeneration: input.AcceptedGeneration, ImageCacheSnapshotID: input.ImageCacheSnapshotID, Tree: input.Tree, Source: input.Source, Profile: input.Profile, Entrypoint: input.Entrypoint, Platform: input.Platform, ToolchainDigest: input.ToolchainDigest, CandidateGateSourceSHA256: input.CandidateGateSourceSHA256, CandidateGateToolchainSHA256: input.CandidateGateToolchainSHA256, Inventory: input.Inventory, Calibration: input.Calibration, Force: input.Force, RunnerIdentityDigest: input.RunnerIdentityDigest, RunnerImage: input.RunnerImage, CalibrationResource: input.CalibrationResource}
}

func (input calibrationCheckpointInput) expand() RunInput {
	return RunInput{AgentTokenDigest: input.AgentTokenDigest, AcceptedGeneration: input.AcceptedGeneration, ImageCacheSnapshotID: input.ImageCacheSnapshotID, Tree: input.Tree, Source: input.Source, Profile: input.Profile, Entrypoint: input.Entrypoint, Platform: input.Platform, ToolchainDigest: input.ToolchainDigest, CandidateGateSourceSHA256: input.CandidateGateSourceSHA256, CandidateGateToolchainSHA256: input.CandidateGateToolchainSHA256, Inventory: input.Inventory, Calibration: input.Calibration, Force: input.Force, RunnerIdentityDigest: input.RunnerIdentityDigest, RunnerImage: input.RunnerImage, CalibrationResource: input.CalibrationResource}
}

func compactCalibrationCheckpointResult(result RunResult) *calibrationCheckpointResult {
	return &calibrationCheckpointResult{AgentTokenDigest: result.AgentTokenDigest, AcceptedGeneration: result.AcceptedGeneration, ImageCacheSnapshotID: result.ImageCacheSnapshotID, JobID: result.JobID, PlanDigest: result.PlanDigest, CatalogDigest: result.CatalogDigest, SourceTreeSHA: result.SourceTreeSHA, CandidateGateSourceSHA256: result.CandidateGateSourceSHA256, CandidateGateToolchainSHA256: result.CandidateGateToolchainSHA256, Entrypoint: result.Entrypoint, Profile: result.Profile, Force: result.Force, Status: result.Status, Authoritative: result.Authoritative, CleanupComplete: result.CleanupComplete, CalibrationResourceClassID: result.CalibrationResourceClassID, CalibrationResourceCPU: result.CalibrationResourceCPU, CalibrationResourceMemoryGiB: result.CalibrationResourceMemoryGiB, CompletedAt: result.CompletedAt}
}

func (result calibrationCheckpointResult) expand() RunResult {
	return RunResult{AgentTokenDigest: result.AgentTokenDigest, AcceptedGeneration: result.AcceptedGeneration, ImageCacheSnapshotID: result.ImageCacheSnapshotID, JobID: result.JobID, PlanDigest: result.PlanDigest, CatalogDigest: result.CatalogDigest, SourceTreeSHA: result.SourceTreeSHA, CandidateGateSourceSHA256: result.CandidateGateSourceSHA256, CandidateGateToolchainSHA256: result.CandidateGateToolchainSHA256, Entrypoint: result.Entrypoint, Profile: result.Profile, Force: result.Force, Status: result.Status, Authoritative: result.Authoritative, CleanupComplete: result.CleanupComplete, CalibrationResourceClassID: result.CalibrationResourceClassID, CalibrationResourceCPU: result.CalibrationResourceCPU, CalibrationResourceMemoryGiB: result.CalibrationResourceMemoryGiB, CompletedAt: result.CompletedAt}
}

// validateCalibrationCheckpointDocument 验证所有场景的持久化状态和完整身份。
func validateCalibrationCheckpointDocument(document calibrationCheckpointDocument) error {
	for scenario, state := range document.Scenarios {
		if strings.TrimSpace(scenario) == "" || !state.Started {
			return errors.New("calibration checkpoint contains invalid scenario state")
		}
		if !state.Completed {
			if state.Input != nil || state.Result != nil {
				return fmt.Errorf("calibration checkpoint scenario %q retained partial execution payload", scenario)
			}
			continue
		}
		if state.Input == nil || state.Result == nil {
			return fmt.Errorf("calibration checkpoint scenario %q completed without identity", scenario)
		}
		if err := validateCompletedCalibrationCheckpoint(*state.Input, *state.Result); err != nil {
			return fmt.Errorf("calibration checkpoint scenario %q: %w", scenario, err)
		}
	}
	return nil
}

// validateCompletedCalibrationCheckpoint 要求完成结果与输入的严格身份完全一致。
func validateCompletedCalibrationCheckpoint(input calibrationCheckpointInput, result calibrationCheckpointResult) error {
	if err := validateCompletedCalibrationCheckpointInput(input); err != nil {
		return err
	}
	if err := validateCompletedCalibrationCheckpointResult(result); err != nil {
		return err
	}
	if !calibrationCheckpointResultMatchesInput(input, result) {
		return errors.New("completed calibration checkpoint result does not match its input")
	}
	return nil
}

// validateCompletedCalibrationCheckpointInput 验证已完成校准输入的全部身份字段。
func validateCompletedCalibrationCheckpointInput(input calibrationCheckpointInput) error {
	if input.AcceptedGeneration == 0 || !input.Calibration {
		return errors.New("completed calibration checkpoint input identity is incomplete")
	}
	if !validCalibrationCheckpointTokenDigest(input.AgentTokenDigest) || !validImageCacheIdentifier(input.ImageCacheSnapshotID) || hasBlankCalibrationCheckpointIdentity(input.Tree, input.RunnerIdentityDigest, input.RunnerImage, input.CandidateGateSourceSHA256, input.CandidateGateToolchainSHA256) {
		return errors.New("completed calibration checkpoint input identity is incomplete")
	}
	if cicontract.ValidateTargetPlatform(input.Platform) != nil {
		return errors.New("completed calibration checkpoint input identity is incomplete")
	}
	if cicontract.ValidateCalibrationResources(input.CalibrationResource.ID, input.CalibrationResource.VCPU, input.CalibrationResource.MemoryGiB) != nil {
		return errors.New("completed calibration checkpoint input identity is incomplete")
	}
	return nil
}

// validateCompletedCalibrationCheckpointResult 验证已完成校准结果的全部身份字段。
func validateCompletedCalibrationCheckpointResult(result calibrationCheckpointResult) error {
	if !validCompletedCalibrationCheckpointResultIdentity(result) {
		return errors.New("completed calibration checkpoint result identity is incomplete")
	}
	if result.Entrypoint == "" || result.Profile == "" || result.Status != gatecontract.ResultStatusPassed || !result.Authoritative || !result.CleanupComplete || result.CompletedAt.IsZero() {
		return errors.New("completed calibration checkpoint result identity is incomplete")
	}
	if cicontract.ValidateCalibrationResources(result.CalibrationResourceClassID, result.CalibrationResourceCPU, result.CalibrationResourceMemoryGiB) != nil {
		return errors.New("completed calibration checkpoint result calibration resource identity is incomplete")
	}
	return nil
}

// validCompletedCalibrationCheckpointResultIdentity 检查完成结果的代际、代理摘要、快照和严格身份字段。
func validCompletedCalibrationCheckpointResultIdentity(result calibrationCheckpointResult) bool {
	return result.AcceptedGeneration != 0 &&
		validCalibrationCheckpointTokenDigest(result.AgentTokenDigest) &&
		validImageCacheIdentifier(result.ImageCacheSnapshotID) &&
		!hasBlankCalibrationCheckpointIdentity(
			result.JobID,
			result.PlanDigest,
			result.CatalogDigest,
			result.SourceTreeSHA,
			result.CandidateGateSourceSHA256,
			result.CandidateGateToolchainSHA256,
		)
}

// validCalibrationCheckpointTokenDigest 拒绝空或格式无效的代理令牌摘要。
func validCalibrationCheckpointTokenDigest(agentTokenDigest string) bool {
	return cicontract.ValidateAgentTokenDigest(agentTokenDigest) == nil
}

// hasBlankCalibrationCheckpointIdentity 检查严格身份字段是否包含空白值。
func hasBlankCalibrationCheckpointIdentity(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return true
		}
	}
	return false
}

// calibrationCheckpointResultMatchesInput 比对输入和结果必须相同的候选身份字段。
func calibrationCheckpointResultMatchesInput(input calibrationCheckpointInput, result calibrationCheckpointResult) bool {
	return result.AgentTokenDigest == input.AgentTokenDigest &&
		result.AcceptedGeneration == input.AcceptedGeneration &&
		result.ImageCacheSnapshotID == input.ImageCacheSnapshotID &&
		result.Force == input.Force &&
		result.SourceTreeSHA == input.Tree &&
		result.CandidateGateSourceSHA256 == input.CandidateGateSourceSHA256 &&
		result.CandidateGateToolchainSHA256 == input.CandidateGateToolchainSHA256 &&
		calibrationCheckpointResultMatchesResourceIdentity(input, result) &&
		result.Entrypoint == input.Entrypoint &&
		result.Profile == input.Profile
}

// calibrationCheckpointResultMatchesResourceIdentity 比对校准资源等级和规格。
func calibrationCheckpointResultMatchesResourceIdentity(input calibrationCheckpointInput, result calibrationCheckpointResult) bool {
	return result.CalibrationResourceClassID == input.CalibrationResource.ID &&
		result.CalibrationResourceCPU == input.CalibrationResource.VCPU &&
		result.CalibrationResourceMemoryGiB == input.CalibrationResource.MemoryGiB
}
