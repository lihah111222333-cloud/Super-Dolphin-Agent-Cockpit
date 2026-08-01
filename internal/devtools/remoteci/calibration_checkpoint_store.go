package remoteci

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const (
	calibrationCheckpointSchemaVersion            uint32 = 5
	previousCalibrationCheckpointSchemaVersion    uint32 = 3
	prePreviousCalibrationCheckpointSchemaVersion uint32 = 2
	legacyCalibrationCheckpointSchemaVersion      uint32 = 1
	calibrationCheckpointMaxBytes                        = 4 << 20
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
	Tree                 string                         `json:"tree"`
	Source               gatecontract.SourceSpec        `json:"source"`
	Profile              gatecontract.Profile           `json:"profile"`
	Entrypoint           gatecontract.CIEntrypointID    `json:"entrypoint"`
	Platform             string                         `json:"platform"`
	ToolchainDigest      string                         `json:"toolchain_digest"`
	Inventory            gatecontract.WorkloadInventory `json:"inventory"`
	Calibration          bool                           `json:"calibration"`
	RunnerIdentityDigest string                         `json:"runner_identity_digest"`
	RunnerImage          string                         `json:"runner_image"`
}

type calibrationCheckpointResult struct {
	JobID                                   string                      `json:"job_id"`
	PlanDigest                              string                      `json:"plan_digest"`
	CatalogDigest                           string                      `json:"catalog_digest"`
	SourceTreeSHA                           string                      `json:"source_tree_sha"`
	CandidateCLIManifestSHA256              string                      `json:"candidate_cli_manifest_sha256"`
	CandidateTestBinaryReceiptBindingDigest string                      `json:"candidate_test_binary_receipt_binding_digest"`
	Entrypoint                              gatecontract.CIEntrypointID `json:"entrypoint"`
	Profile                                 gatecontract.Profile        `json:"profile"`
	Status                                  gatecontract.ResultStatus   `json:"status"`
	Authoritative                           bool                        `json:"authoritative"`
	CleanupComplete                         bool                        `json:"cleanup_complete"`
	CompletedAt                             time.Time                   `json:"completed_at"`
}

// Validate 拒绝版本漂移、空身份和无法恢复的场景状态。
func (document *calibrationCheckpointDocument) Validate() error {
	if document.SchemaVersion != calibrationCheckpointSchemaVersion ||
		strings.TrimSpace(document.Identity) == "" || document.Scenarios == nil {
		return errors.New("calibration checkpoint schema or identity is invalid")
	}
	return validateCalibrationCheckpointDocument(*document)
}

// CalibrationCheckpoint 保存校准场景的真实执行进度，失败重试时复用已通过 workload。
type CalibrationCheckpoint struct {
	path     string
	document calibrationCheckpointDocument
}

// NewCalibrationCheckpoint 加载与当前候选身份一致的校准断点。
func NewCalibrationCheckpoint(path string, identity string) (*CalibrationCheckpoint, error) {
	if strings.TrimSpace(path) == "" || strings.TrimSpace(identity) == "" {
		return nil, errors.New("calibration checkpoint path and identity are required")
	}
	document, replace, err := loadCalibrationCheckpoint(path, identity)
	if err != nil {
		return nil, err
	}
	checkpoint := &CalibrationCheckpoint{path: path, document: document}
	if replace {
		if err := checkpoint.persist(); err != nil {
			return nil, err
		}
	}
	return checkpoint, nil
}

// loadCalibrationCheckpoint 加载当前身份的断点，并迁移已支持的旧版本。
func loadCalibrationCheckpoint(path string, identity string) (calibrationCheckpointDocument, bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return newCalibrationCheckpointDocument(identity), false, nil
	}
	if err != nil {
		return calibrationCheckpointDocument{}, false, err
	}
	version, storedIdentity, err := readCalibrationCheckpointHeader(path)
	if err != nil {
		return calibrationCheckpointDocument{}, false, err
	}
	if storedIdentity != identity {
		return newCalibrationCheckpointDocument(identity), true, nil
	}
	return loadVersionedCalibrationCheckpoint(path, identity, version, info.Size())
}

// loadVersionedCalibrationCheckpoint 加载当前版本或迁移受支持的历史版本。
func loadVersionedCalibrationCheckpoint(path, identity string, version uint32, size int64) (calibrationCheckpointDocument, bool, error) {
	if version != calibrationCheckpointSchemaVersion {
		// Older checkpoint documents lack the candidate-binary binding. They
		// may retain progress only after a fresh authoritative run, never reuse.
		return newCalibrationCheckpointDocument(identity), true, nil
	}
	if size > calibrationCheckpointMaxBytes {
		return calibrationCheckpointDocument{}, false, fmt.Errorf("calibration checkpoint exceeds %d bytes", calibrationCheckpointMaxBytes)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return calibrationCheckpointDocument{}, false, err
	}
	var document calibrationCheckpointDocument
	if err := gatecontract.DecodeStrictJSON(content, &document); err != nil {
		return calibrationCheckpointDocument{}, false, fmt.Errorf("decode calibration checkpoint: %w", err)
	}
	if err := document.Validate(); err != nil {
		return calibrationCheckpointDocument{}, false, err
	}
	return document, false, nil
}

func newCalibrationCheckpointDocument(identity string) calibrationCheckpointDocument {
	return calibrationCheckpointDocument{
		SchemaVersion: calibrationCheckpointSchemaVersion,
		Identity:      identity,
		Scenarios:     make(map[string]calibrationScenarioState),
	}
}

// validateCalibrationCheckpointDocument 严格校验断点中的场景全集和已观察结果。
func validateCalibrationCheckpointDocument(document calibrationCheckpointDocument) error {
	for scenario, state := range document.Scenarios {
		if strings.TrimSpace(scenario) == "" {
			return errors.New("calibration checkpoint contains an empty scenario")
		}
		if !state.Started {
			return fmt.Errorf("calibration checkpoint scenario %q has invalid observed state", scenario)
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

// Completed 返回已经权威完成的场景输入与结果。
func (checkpoint *CalibrationCheckpoint) Completed(scenario string) (RunInput, RunResult, bool) {
	state, ok := checkpoint.document.Scenarios[scenario]
	if !ok || !state.Completed || state.Input == nil || state.Result == nil {
		return RunInput{}, RunResult{}, false
	}
	return state.Input.expand(), state.Result.expand(), true
}

// Reopen 清除场景的完成载荷，但保留已开始状态供缓存恢复执行。
func (checkpoint *CalibrationCheckpoint) Reopen(scenario string) error {
	state, ok := checkpoint.document.Scenarios[scenario]
	if !ok || !state.Completed {
		return nil
	}
	checkpoint.document.Scenarios[scenario] = calibrationScenarioState{Started: true}
	return checkpoint.persist()
}

// Observe 原子保存包含成功时长样本的执行结果。
func (checkpoint *CalibrationCheckpoint) Observe(
	scenario string,
	input RunInput,
	result RunResult,
	completed bool,
) error {
	if strings.TrimSpace(scenario) == "" {
		return errors.New("calibration checkpoint scenario is required")
	}
	if !input.Calibration {
		return errors.New("calibration checkpoint input is not a calibration run")
	}
	if completed {
		compactInput := compactCalibrationCheckpointInput(input)
		compactResult := compactCalibrationCheckpointResult(result)
		if err := validateCompletedCalibrationCheckpoint(*compactInput, *compactResult); err != nil {
			return err
		}
	}
	if len(result.DurationSamples) == 0 {
		return checkpoint.observeCachedCompletion(scenario, input, result, completed)
	}
	state := calibrationScenarioState{Started: true, Completed: completed}
	if completed {
		state.Input = compactCalibrationCheckpointInput(input)
		state.Result = compactCalibrationCheckpointResult(result)
	}
	checkpoint.document.Scenarios[scenario] = state
	return checkpoint.persist()
}

func (checkpoint *CalibrationCheckpoint) observeCachedCompletion(
	scenario string,
	input RunInput,
	result RunResult,
	completed bool,
) error {
	state, ok := checkpoint.document.Scenarios[scenario]
	if !ok || !state.Started || !completed {
		return nil
	}
	state.Completed = true
	state.Input = compactCalibrationCheckpointInput(input)
	state.Result = compactCalibrationCheckpointResult(result)
	checkpoint.document.Scenarios[scenario] = state
	return checkpoint.persist()
}

func compactCalibrationCheckpointInput(input RunInput) *calibrationCheckpointInput {
	return &calibrationCheckpointInput{
		Tree: input.Tree, Source: input.Source, Profile: input.Profile, Entrypoint: input.Entrypoint,
		Platform: input.Platform, ToolchainDigest: input.ToolchainDigest, Inventory: input.Inventory,
		Calibration: input.Calibration, RunnerIdentityDigest: input.RunnerIdentityDigest,
		RunnerImage: input.RunnerImage,
	}
}

func (input calibrationCheckpointInput) expand() RunInput {
	return RunInput{
		Tree: input.Tree, Source: input.Source, Profile: input.Profile, Entrypoint: input.Entrypoint,
		Platform: input.Platform, ToolchainDigest: input.ToolchainDigest, Inventory: input.Inventory,
		Calibration: input.Calibration, RunnerIdentityDigest: input.RunnerIdentityDigest,
		RunnerImage: input.RunnerImage,
	}
}

func compactCalibrationCheckpointResult(result RunResult) *calibrationCheckpointResult {
	return &calibrationCheckpointResult{
		JobID: result.JobID, PlanDigest: result.PlanDigest, CatalogDigest: result.CatalogDigest,
		SourceTreeSHA: result.SourceTreeSHA, Entrypoint: result.Entrypoint, Profile: result.Profile,
		CandidateCLIManifestSHA256:              result.CandidateCLIManifestSHA256,
		CandidateTestBinaryReceiptBindingDigest: result.CandidateTestBinaryReceiptBindingDigest,
		Status:                                  result.Status, Authoritative: result.Authoritative,
		CleanupComplete: result.CleanupComplete, CompletedAt: result.CompletedAt,
	}
}

func (result calibrationCheckpointResult) expand() RunResult {
	return RunResult{
		JobID: result.JobID, PlanDigest: result.PlanDigest, CatalogDigest: result.CatalogDigest,
		SourceTreeSHA: result.SourceTreeSHA, Entrypoint: result.Entrypoint, Profile: result.Profile,
		CandidateCLIManifestSHA256:              result.CandidateCLIManifestSHA256,
		CandidateTestBinaryReceiptBindingDigest: result.CandidateTestBinaryReceiptBindingDigest,
		Status:                                  result.Status, Authoritative: result.Authoritative,
		CleanupComplete: result.CleanupComplete, CompletedAt: result.CompletedAt,
	}
}

// validateCompletedCalibrationCheckpoint 校验完成场景的输入、结果和身份绑定。
func validateCompletedCalibrationCheckpoint(
	input calibrationCheckpointInput,
	result calibrationCheckpointResult,
) error {
	if !validCompletedCalibrationInput(input) {
		return errors.New("completed calibration checkpoint input identity is incomplete")
	}
	if !validCompletedCalibrationResult(result) {
		return errors.New("completed calibration checkpoint result identity is incomplete")
	}
	if !matchesCompletedCalibrationInput(input, result) {
		return errors.New("completed calibration checkpoint result does not match its input")
	}
	return nil
}

// validCompletedCalibrationInput 校验可恢复完成记录的输入身份。
func validCompletedCalibrationInput(input calibrationCheckpointInput) bool {
	return input.Calibration && strings.TrimSpace(input.Tree) != "" &&
		strings.TrimSpace(input.RunnerIdentityDigest) != "" && strings.TrimSpace(input.RunnerImage) != ""
}

// validCompletedCalibrationResult 校验可恢复完成记录的权威结果。
func validCompletedCalibrationResult(result calibrationCheckpointResult) bool {
	return validCompletedCalibrationResultIdentity(result) && validCompletedCalibrationResultStatus(result)
}

// validCompletedCalibrationResultIdentity 判断已完成结果的不可变标识完整。
func validCompletedCalibrationResultIdentity(result calibrationCheckpointResult) bool {
	return strings.TrimSpace(result.JobID) != "" && strings.TrimSpace(result.PlanDigest) != "" &&
		strings.TrimSpace(result.CatalogDigest) != "" && strings.TrimSpace(result.SourceTreeSHA) != "" &&
		validObjectDigest(result.CandidateCLIManifestSHA256) &&
		remoteDigestPattern.MatchString(result.CandidateTestBinaryReceiptBindingDigest)
}

// validCompletedCalibrationResultStatus 判断已完成结果具备可接受终态。
func validCompletedCalibrationResultStatus(result calibrationCheckpointResult) bool {
	return result.Entrypoint != "" && result.Profile != "" && result.Status == gatecontract.ResultStatusPassed &&
		result.Authoritative && result.CleanupComplete && !result.CompletedAt.IsZero()
}

// matchesCompletedCalibrationInput 校验完成结果仍绑定到原始输入。
func matchesCompletedCalibrationInput(input calibrationCheckpointInput, result calibrationCheckpointResult) bool {
	return result.SourceTreeSHA == input.Tree && result.Entrypoint == input.Entrypoint && result.Profile == input.Profile
}

// readCalibrationCheckpointHeader 只读取并验证文档的版本和身份头部。
func readCalibrationCheckpointHeader(path string) (uint32, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	if err := expectJSONDelimiter(decoder, '{'); err != nil {
		return 0, "", err
	}
	var version uint32
	var identity string
	seen := make(map[string]struct{}, 2)
	for decoder.More() {
		key, err := nextJSONObjectKey(decoder, seen)
		if err != nil {
			return 0, "", err
		}
		switch key {
		case "schema_version":
			err = decoder.Decode(&version)
		case "identity":
			err = decoder.Decode(&identity)
		default:
			return 0, "", errors.New("calibration checkpoint header must precede scenario payload")
		}
		if err != nil {
			return 0, "", err
		}
		if version != 0 && strings.TrimSpace(identity) != "" {
			return version, identity, nil
		}
	}
	return 0, "", errors.New("calibration checkpoint header is incomplete")
}

// migrateLegacyCalibrationCheckpointDocument 流式迁移旧版本断点文档。
func migrateLegacyCalibrationCheckpointDocument(
	decoder *json.Decoder,
	identity string,
	expectedVersion uint32,
) (calibrationCheckpointDocument, error) {
	if err := expectJSONDelimiter(decoder, '{'); err != nil {
		return calibrationCheckpointDocument{}, err
	}
	document := newCalibrationCheckpointDocument(identity)
	seen := make(map[string]struct{}, 3)
	var version uint32
	var storedIdentity string
	for decoder.More() {
		key, err := nextJSONObjectKey(decoder, seen)
		if err != nil {
			return calibrationCheckpointDocument{}, err
		}
		err = migrateLegacyCalibrationCheckpointField(decoder, key, &version, &storedIdentity, &document)
		if err != nil {
			return calibrationCheckpointDocument{}, err
		}
	}
	if err := expectJSONDelimiter(decoder, '}'); err != nil {
		return calibrationCheckpointDocument{}, err
	}
	if err := rejectTrailingJSON(decoder); err != nil {
		return calibrationCheckpointDocument{}, err
	}
	if err := validateLegacyCalibrationCheckpointDocument(
		document,
		version,
		expectedVersion,
		storedIdentity,
		identity,
		seen,
	); err != nil {
		return calibrationCheckpointDocument{}, err
	}
	return document, nil
}

func validateLegacyCalibrationCheckpointDocument(
	document calibrationCheckpointDocument,
	version uint32,
	expectedVersion uint32,
	storedIdentity string,
	identity string,
	seen map[string]struct{},
) error {
	if version != expectedVersion || storedIdentity != identity || len(seen) != 3 {
		return errors.New("legacy calibration checkpoint identity or schema is invalid")
	}
	return document.Validate()
}

func migrateLegacyCalibrationCheckpointField(
	decoder *json.Decoder,
	key string,
	version *uint32,
	identity *string,
	document *calibrationCheckpointDocument,
) error {
	switch key {
	case "schema_version":
		return decoder.Decode(version)
	case "identity":
		return decoder.Decode(identity)
	case "scenarios":
		scenarios, err := migrateLegacyCalibrationScenarios(decoder)
		if err != nil {
			return err
		}
		document.Scenarios = scenarios
		return nil
	default:
		return fmt.Errorf("legacy calibration checkpoint contains unknown field %q", key)
	}
}

// migrateLegacyCalibrationScenarios 严格迁移旧场景对象。
func migrateLegacyCalibrationScenarios(decoder *json.Decoder) (map[string]calibrationScenarioState, error) {
	if err := expectJSONDelimiter(decoder, '{'); err != nil {
		return nil, err
	}
	states := make(map[string]calibrationScenarioState)
	seen := make(map[string]struct{})
	for decoder.More() {
		scenario, err := nextJSONObjectKey(decoder, seen)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(scenario) == "" {
			return nil, errors.New("legacy calibration checkpoint contains an empty scenario")
		}
		state, err := migrateLegacyCalibrationScenario(decoder)
		if err != nil {
			return nil, fmt.Errorf("migrate legacy calibration scenario %q: %w", scenario, err)
		}
		states[scenario] = state
	}
	if err := expectJSONDelimiter(decoder, '}'); err != nil {
		return nil, err
	}
	return states, nil
}

// migrateLegacyCalibrationScenario 严格读取旧场景状态并仅保留可安全恢复的开始标记。
func migrateLegacyCalibrationScenario(decoder *json.Decoder) (calibrationScenarioState, error) {
	if err := expectJSONDelimiter(decoder, '{'); err != nil {
		return calibrationScenarioState{}, err
	}
	seen := make(map[string]struct{}, 4)
	var (
		started   bool
		completed bool
	)
	for decoder.More() {
		if err := decodeLegacyCalibrationScenarioField(decoder, seen, &started, &completed); err != nil {
			return calibrationScenarioState{}, err
		}
	}
	if err := expectJSONDelimiter(decoder, '}'); err != nil {
		return calibrationScenarioState{}, err
	}
	_, hasStarted := seen["started"]
	_, hasCompleted := seen["completed"]
	_, hasInput := seen["input"]
	_, hasResult := seen["result"]
	if !validLegacyCalibrationScenarioFields(hasStarted, hasCompleted, started, hasInput, hasResult) {
		return calibrationScenarioState{}, errors.New("legacy calibration scenario is incomplete")
	}
	if completed != hasInput {
		return calibrationScenarioState{}, errors.New("legacy calibration scenario payload does not match completion")
	}
	return calibrationScenarioState{Started: true}, nil
}

// decodeLegacyCalibrationScenarioField 解码一个旧场景字段并拒绝未知字段。
func decodeLegacyCalibrationScenarioField(decoder *json.Decoder, seen map[string]struct{}, started, completed *bool) error {
	key, err := nextJSONObjectKey(decoder, seen)
	if err != nil {
		return err
	}
	switch key {
	case "started":
		return decoder.Decode(started)
	case "completed":
		return decoder.Decode(completed)
	case "input", "result":
		return skipJSONValue(decoder)
	default:
		return fmt.Errorf("unknown legacy scenario field %q", key)
	}
}

// validLegacyCalibrationScenarioFields 校验旧场景字段集合与开始标记。
func validLegacyCalibrationScenarioFields(hasStarted, hasCompleted, started, hasInput, hasResult bool) bool {
	return hasStarted && hasCompleted && started && hasInput == hasResult
}

func nextJSONObjectKey(decoder *json.Decoder, seen map[string]struct{}) (string, error) {
	token, err := decoder.Token()
	if err != nil {
		return "", err
	}
	key, ok := token.(string)
	if !ok || key == "" {
		return "", errors.New("JSON object key is invalid")
	}
	if _, duplicate := seen[key]; duplicate {
		return "", fmt.Errorf("JSON object field %q is duplicated", key)
	}
	seen[key] = struct{}{}
	return key, nil
}

func expectJSONDelimiter(decoder *json.Decoder, expected json.Delim) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != expected {
		return fmt.Errorf("expected JSON delimiter %q", expected)
	}
	return nil
}

// skipJSONValue 跳过一个完整 JSON 值，同时保持流式结构校验。
func skipJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delimiter {
	case '{':
		return skipJSONObject(decoder)
	case '[':
		return skipJSONArray(decoder)
	default:
		return errors.New("JSON value has an unexpected closing delimiter")
	}
}

// skipJSONObject 跳过对象的每个键和值。
func skipJSONObject(decoder *json.Decoder) error {
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return err
		}
		if _, ok := key.(string); !ok {
			return errors.New("JSON object key is invalid")
		}
		if err := skipJSONValue(decoder); err != nil {
			return err
		}
	}
	return expectJSONDelimiter(decoder, '}')
}

// skipJSONArray 跳过数组的每个值。
func skipJSONArray(decoder *json.Decoder) error {
	for decoder.More() {
		if err := skipJSONValue(decoder); err != nil {
			return err
		}
	}
	return expectJSONDelimiter(decoder, ']')
}

func rejectTrailingJSON(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("calibration checkpoint has trailing JSON")
		}
		return err
	}
	return nil
}

// persist 通过同目录原子替换持久化断点，避免中断留下半份文档。
func (checkpoint *CalibrationCheckpoint) persist() error {
	content, err := json.Marshal(checkpoint.document)
	if err != nil {
		return fmt.Errorf("encode calibration checkpoint: %w", err)
	}
	directory := filepath.Dir(checkpoint.path)
	temporary, err := os.CreateTemp(directory, filepath.Base(checkpoint.path)+".tmp-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, checkpoint.path)
}

// Remove 删除已经接受的校准断点。
func (checkpoint *CalibrationCheckpoint) Remove() error {
	err := os.Remove(checkpoint.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
