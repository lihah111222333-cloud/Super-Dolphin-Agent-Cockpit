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
	_ "modernc.org/sqlite"
)

const (
	calibrationCheckpointSchemaVersion uint32 = 1
)

type calibrationCheckpointDocument struct {
	SchemaVersion uint32                              `json:"schema_version"`
	Identity      string                              `json:"identity"`
	Scenarios     map[string]calibrationScenarioState `json:"scenarios"`
}

type calibrationScenarioState struct {
	Started   bool                         `json:"started"`
	Completed bool                         `json:"completed"`
	Input     *calibrationCheckpointInput  `json:"input,omitempty"`
	Result    *calibrationCheckpointResult `json:"result,omitempty"`
}

type calibrationCheckpointInput struct {
	AcceptedGeneration           uint64                         `json:"accepted_generation"`
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
	RunnerIdentityDigest         string                         `json:"runner_identity_digest"`
	RunnerImage                  string                         `json:"runner_image"`
	CalibrationResource          shardresource.Class            `json:"calibration_resource"`
}

func (input *calibrationCheckpointInput) Validate() error {
	if input == nil {
		return errors.New("calibration checkpoint input is required")
	}
	return nil
}

type calibrationCheckpointResult struct {
	AcceptedGeneration           uint64                      `json:"accepted_generation"`
	JobID                        string                      `json:"job_id"`
	PlanDigest                   string                      `json:"plan_digest"`
	CatalogDigest                string                      `json:"catalog_digest"`
	SourceTreeSHA                string                      `json:"source_tree_sha"`
	CandidateGateSourceSHA256    string                      `json:"candidate_gate_source_sha256"`
	CandidateGateToolchainSHA256 string                      `json:"candidate_gate_toolchain_sha256"`
	Entrypoint                   gatecontract.CIEntrypointID `json:"entrypoint"`
	Profile                      gatecontract.Profile        `json:"profile"`
	Status                       gatecontract.ResultStatus   `json:"status"`
	Authoritative                bool                        `json:"authoritative"`
	CleanupComplete              bool                        `json:"cleanup_complete"`
	CompletedAt                  time.Time                   `json:"completed_at"`
}

func (result *calibrationCheckpointResult) Validate() error {
	if result == nil {
		return errors.New("calibration checkpoint result is required")
	}
	return nil
}

// Validate 拒绝版本漂移、空身份和无法恢复的场景状态。
func (document *calibrationCheckpointDocument) Validate() error {
	if document.SchemaVersion != calibrationCheckpointSchemaVersion ||
		strings.TrimSpace(document.Identity) == "" || document.Scenarios == nil {
		return errors.New("calibration checkpoint schema or identity is invalid")
	}
	return validateCalibrationCheckpointDocument(*document)
}

// CalibrationCheckpoint 将校准场景进度持久化在 duration ledger 的 SQLite 权威库中。
type CalibrationCheckpoint struct {
	store              *gatecontract.DurationLedgerStore
	identity           string
	acceptedGeneration uint64
}

// NewCalibrationCheckpoint 打开 duration ledger SQLite 权威库。
func NewCalibrationCheckpoint(store *gatecontract.DurationLedgerStore, identity string, acceptedGeneration uint64) (*CalibrationCheckpoint, error) {
	if store == nil || strings.TrimSpace(identity) == "" || acceptedGeneration == 0 {
		return nil, errors.New("calibration checkpoint duration ledger store, identity, and accepted generation are required")
	}
	checkpoint := &CalibrationCheckpoint{
		store:              store,
		identity:           identity,
		acceptedGeneration: acceptedGeneration,
	}
	if err := checkpoint.ensure(); err != nil {
		return nil, err
	}
	return checkpoint, nil
}

func (checkpoint *CalibrationCheckpoint) ensure() error {
	record, found, err := checkpoint.store.LoadCalibrationCheckpoint(checkpoint.identity)
	if err != nil {
		return err
	}
	if found && record.AcceptedGeneration != checkpoint.acceptedGeneration {
		return errors.New("calibration checkpoint accepted generation does not match authority")
	}
	return nil
}

// Completed 返回已经权威完成的场景输入与结果。
func (checkpoint *CalibrationCheckpoint) Completed(scenario string) (RunInput, RunResult, bool) {
	document, err := checkpoint.loadDocument()
	if err != nil {
		return RunInput{}, RunResult{}, false
	}
	state, ok := document.Scenarios[scenario]
	if !ok || !state.Completed || state.Input == nil || state.Result == nil {
		return RunInput{}, RunResult{}, false
	}
	return state.Input.expand(), state.Result.expand(), true
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
	if strings.TrimSpace(scenario) == "" {
		return errors.New("calibration checkpoint scenario is required")
	}
	if !input.Calibration {
		return errors.New("calibration checkpoint input is not a calibration run")
	}
	if input.AcceptedGeneration != checkpoint.acceptedGeneration || result.AcceptedGeneration != checkpoint.acceptedGeneration {
		return errors.New("calibration checkpoint execution accepted generation does not match authority")
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

func (checkpoint *CalibrationCheckpoint) loadDocument() (calibrationCheckpointDocument, error) {
	document := calibrationCheckpointDocument{SchemaVersion: calibrationCheckpointSchemaVersion, Identity: checkpoint.identity, Scenarios: make(map[string]calibrationScenarioState)}
	record, found, err := checkpoint.store.LoadCalibrationCheckpoint(checkpoint.identity)
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
	return checkpoint.store.CompareAndSwapCalibrationCheckpointScenario(checkpoint.identity, calibrationCheckpointSchemaVersion, checkpoint.acceptedGeneration, expectedRecord, nextRecord)
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
	return checkpoint.store.DeleteCalibrationCheckpoint(checkpoint.identity)
}

func compactCalibrationCheckpointInput(input RunInput) *calibrationCheckpointInput {
	return &calibrationCheckpointInput{AcceptedGeneration: input.AcceptedGeneration, Tree: input.Tree, Source: input.Source, Profile: input.Profile, Entrypoint: input.Entrypoint, Platform: input.Platform, ToolchainDigest: input.ToolchainDigest, CandidateGateSourceSHA256: input.CandidateGateSourceSHA256, CandidateGateToolchainSHA256: input.CandidateGateToolchainSHA256, Inventory: input.Inventory, Calibration: input.Calibration, RunnerIdentityDigest: input.RunnerIdentityDigest, RunnerImage: input.RunnerImage, CalibrationResource: input.CalibrationResource}
}

func (input calibrationCheckpointInput) expand() RunInput {
	return RunInput{AcceptedGeneration: input.AcceptedGeneration, Tree: input.Tree, Source: input.Source, Profile: input.Profile, Entrypoint: input.Entrypoint, Platform: input.Platform, ToolchainDigest: input.ToolchainDigest, CandidateGateSourceSHA256: input.CandidateGateSourceSHA256, CandidateGateToolchainSHA256: input.CandidateGateToolchainSHA256, Inventory: input.Inventory, Calibration: input.Calibration, RunnerIdentityDigest: input.RunnerIdentityDigest, RunnerImage: input.RunnerImage, CalibrationResource: input.CalibrationResource}
}

func compactCalibrationCheckpointResult(result RunResult) *calibrationCheckpointResult {
	return &calibrationCheckpointResult{AcceptedGeneration: result.AcceptedGeneration, JobID: result.JobID, PlanDigest: result.PlanDigest, CatalogDigest: result.CatalogDigest, SourceTreeSHA: result.SourceTreeSHA, CandidateGateSourceSHA256: result.CandidateGateSourceSHA256, CandidateGateToolchainSHA256: result.CandidateGateToolchainSHA256, Entrypoint: result.Entrypoint, Profile: result.Profile, Status: result.Status, Authoritative: result.Authoritative, CleanupComplete: result.CleanupComplete, CompletedAt: result.CompletedAt}
}

func (result calibrationCheckpointResult) expand() RunResult {
	return RunResult{AcceptedGeneration: result.AcceptedGeneration, JobID: result.JobID, PlanDigest: result.PlanDigest, CatalogDigest: result.CatalogDigest, SourceTreeSHA: result.SourceTreeSHA, CandidateGateSourceSHA256: result.CandidateGateSourceSHA256, CandidateGateToolchainSHA256: result.CandidateGateToolchainSHA256, Entrypoint: result.Entrypoint, Profile: result.Profile, Status: result.Status, Authoritative: result.Authoritative, CleanupComplete: result.CleanupComplete, CompletedAt: result.CompletedAt}
}

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

func validateCompletedCalibrationCheckpoint(input calibrationCheckpointInput, result calibrationCheckpointResult) error {
	if input.AcceptedGeneration == 0 || !input.Calibration || strings.TrimSpace(input.Tree) == "" || strings.TrimSpace(input.RunnerIdentityDigest) == "" || strings.TrimSpace(input.RunnerImage) == "" || strings.TrimSpace(input.CandidateGateSourceSHA256) == "" || strings.TrimSpace(input.CandidateGateToolchainSHA256) == "" || cicontract.ValidateTargetPlatform(input.Platform) != nil || cicontract.ValidateCalibrationResources(input.CalibrationResource.ID, input.CalibrationResource.VCPU, input.CalibrationResource.MemoryGiB) != nil {
		return errors.New("completed calibration checkpoint input identity is incomplete")
	}
	if result.AcceptedGeneration == 0 || strings.TrimSpace(result.JobID) == "" || strings.TrimSpace(result.PlanDigest) == "" || strings.TrimSpace(result.CatalogDigest) == "" || strings.TrimSpace(result.SourceTreeSHA) == "" || strings.TrimSpace(result.CandidateGateSourceSHA256) == "" || strings.TrimSpace(result.CandidateGateToolchainSHA256) == "" || result.Entrypoint == "" || result.Profile == "" || result.Status != gatecontract.ResultStatusPassed || !result.Authoritative || !result.CleanupComplete || result.CompletedAt.IsZero() {
		return errors.New("completed calibration checkpoint result identity is incomplete")
	}
	if result.AcceptedGeneration != input.AcceptedGeneration || result.SourceTreeSHA != input.Tree || result.CandidateGateSourceSHA256 != input.CandidateGateSourceSHA256 || result.CandidateGateToolchainSHA256 != input.CandidateGateToolchainSHA256 || result.Entrypoint != input.Entrypoint || result.Profile != input.Profile {
		return errors.New("completed calibration checkpoint result does not match its input")
	}
	return nil
}
