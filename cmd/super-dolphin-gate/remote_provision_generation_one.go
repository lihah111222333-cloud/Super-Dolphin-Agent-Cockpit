package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

const remoteGenerationOneProvisionReceiptLimit = 16 << 20

type remoteGenerationOneProvisionOptions struct {
	ConfigPath  string
	LedgerPath  string
	ReceiptPath string
}

type remoteGenerationOneProvisionResult struct {
	SchemaVersion        uint32 `json:"schema_version"`
	Authority            string `json:"authority"`
	Generation           uint64 `json:"generation"`
	StateSHA256          string `json:"state_sha256"`
	ReceiptSHA256        string `json:"receipt_sha256"`
	ImageCacheID         string `json:"image_cache_id"`
	ImageCacheSnapshotID string `json:"image_cache_snapshot_id"`
}

type remoteGenerationOneProvisionInput struct {
	Config      remoteRunConfig
	ReceiptJSON []byte
	Receipt     cicontract.GenerationOneProvisionReceipt
}

// parseRemoteGenerationOneProvisionOptions 严格要求外部回执，禁止把首代供给当作 refresh 或 normal fallback。
func parseRemoteGenerationOneProvisionOptions(args []string) (remoteGenerationOneProvisionOptions, error) {
	options := remoteGenerationOneProvisionOptions{}
	flags := flag.NewFlagSet("remote provision-generation-one", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.ConfigPath, "config", "", "remote CI config path")
	flags.StringVar(&options.LedgerPath, "ledger", "", "remote baseline and duration ledger SQLite authority path")
	flags.StringVar(&options.ReceiptPath, "receipt", "", "external ECI generation-one receipt path")
	if err := flags.Parse(args); err != nil {
		return options, protocolError("parse remote provision-generation-one flags: %v", err)
	}
	if flags.NArg() != 0 || strings.TrimSpace(options.ConfigPath) == "" || strings.TrimSpace(options.ReceiptPath) == "" {
		return options, protocolError("remote provision-generation-one requires --config, --receipt, and valid optional flags")
	}
	configPath, err := filepath.Abs(options.ConfigPath)
	if err != nil {
		return options, protocolError("resolve generation-one config path: %v", err)
	}
	options.ConfigPath = configPath
	if err := normalizeRemoteSQLiteAuthority(options.ConfigPath, &options.LedgerPath); err != nil {
		return options, err
	}
	return options, nil
}

// runRemoteGenerationOneProvision 导入外部回执，复核 ECI Ready 事实并执行唯一首代 INSERT。
func runRemoteGenerationOneProvision(args []string, stdout io.Writer) error {
	options, err := parseRemoteGenerationOneProvisionOptions(args)
	if err != nil {
		return err
	}
	input, err := loadRemoteGenerationOneProvisionInput(options)
	if err != nil {
		return err
	}
	if err := verifyRemoteGenerationOneProvisionECI(input.Config, input.Receipt); err != nil {
		return err
	}
	record, err := initializeRemoteGenerationOneProvision(options, input)
	if err != nil {
		return err
	}
	return writeRemoteGenerationOneProvisionResult(stdout, input.Receipt, record)
}

// loadRemoteGenerationOneProvisionInput 读取并验证配置、回执、BaselineState 与固定校准规格。
func loadRemoteGenerationOneProvisionInput(options remoteGenerationOneProvisionOptions) (remoteGenerationOneProvisionInput, error) {
	config, err := loadRemoteRunConfig(options.ConfigPath)
	if err != nil {
		return remoteGenerationOneProvisionInput{}, protocolError("load remote CI config for generation-one provision: %v", err)
	}
	receiptJSON, err := readGenerationOneProvisionReceipt(options.ReceiptPath)
	if err != nil {
		return remoteGenerationOneProvisionInput{}, protocolError("read generation-one provision receipt: %v", err)
	}
	receipt, err := cicontract.DecodeGenerationOneProvisionReceipt(receiptJSON)
	if err != nil {
		return remoteGenerationOneProvisionInput{}, protocolError("validate generation-one provision receipt: %v", err)
	}
	if err := cicontract.ValidateGenerationOneProvisionChecks(receipt); err != nil {
		return remoteGenerationOneProvisionInput{}, protocolError("validate generation-one provision content checks: %v", err)
	}
	if err := validateRemoteGenerationOneProvisionInput(config, receipt); err != nil {
		return remoteGenerationOneProvisionInput{}, protocolError("validate generation-one provision input: %v", err)
	}
	return remoteGenerationOneProvisionInput{Config: config, ReceiptJSON: receiptJSON, Receipt: receipt}, nil
}

// validateRemoteGenerationOneProvisionInput 校验状态绑定和固定校准资源，拒绝配置漂移。
func validateRemoteGenerationOneProvisionInput(config remoteRunConfig, receipt cicontract.GenerationOneProvisionReceipt) error {
	state, err := decodeGenerationOneProvisionState(receipt)
	if err != nil {
		return fmt.Errorf("validate generation-one baseline state: %w", err)
	}
	if err := validateGenerationOneProvisionStateBinding(receipt, state); err != nil {
		return fmt.Errorf("validate generation-one receipt/state binding: %w", err)
	}
	calibration, err := config.Capacity.ResourcePolicy.ResolveCalibrationClass()
	if err != nil {
		return fmt.Errorf("resolve generation-one calibration resources: %w", err)
	}
	return validateRemoteGenerationOneCalibration(receipt, calibration.ID, calibration.VCPU, calibration.MemoryGiB)
}

// validateRemoteGenerationOneCalibration 确认回执使用远程配置声明的唯一固定规格。
func validateRemoteGenerationOneCalibration(receipt cicontract.GenerationOneProvisionReceipt, classID string, cpu, memoryGiB float64) error {
	if receipt.CalibrationClassID != classID || receipt.CalibrationCPU != cpu || receipt.CalibrationMemoryGiB != memoryGiB {
		return errors.New("generation-one provision receipt calibration resources do not match remote config")
	}
	return nil
}

// verifyRemoteGenerationOneProvisionECI 只 Describe 外部缓存并校验 Ready、snapshot 和 immutable 镜像。
func verifyRemoteGenerationOneProvisionECI(config remoteRunConfig, receipt cicontract.GenerationOneProvisionReceipt) error {
	client, err := newRemoteGenerationOneVerifier(config)
	if err != nil {
		return infrastructureError("create ECI generation-one verifier: %v", err)
	}
	verifyContext, cancel := gateprivate.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	live, err := client.DescribeImageCache(verifyContext, receipt.ImageCacheID)
	if err != nil {
		return infrastructureError("describe generation-one ECI ImageCache: %v", err)
	}
	if err := validateGenerationOneLiveImageCache(live, receipt); err != nil {
		return infrastructureError("validate generation-one ECI ImageCache Ready state: %v", err)
	}
	return nil
}

// validateGenerationOneLiveImageCache 将回执完整镜像集合与 ECI 实时事实规范化后精确绑定。
func validateGenerationOneLiveImageCache(live eci.ImageCache, receipt cicontract.GenerationOneProvisionReceipt) error {
	if live.SnapshotID != receipt.ImageCacheSnapshotID || live.Status != receipt.ImageCacheStatus {
		return errors.New("generation-one ECI ImageCache lifecycle does not match receipt")
	}
	if err := eci.ValidateReadyImageCache(live, receipt.ImageCacheID, receipt.ImageCacheName, receipt.Image); err != nil {
		return err
	}
	liveImages := slices.Clone(live.Images)
	receiptImages := slices.Clone(receipt.ImageCacheImages)
	slices.Sort(liveImages)
	slices.Sort(receiptImages)
	if !slices.Equal(liveImages, receiptImages) {
		return errors.New("generation-one ECI ImageCache images do not match receipt")
	}
	return nil
}

// newRemoteGenerationOneVerifier 构造不具备 Create 权限语义的固定 ECI verifier client。
func newRemoteGenerationOneVerifier(config remoteRunConfig) (*eci.Client, error) {
	return eci.New(eci.Config{
		Binary: config.AliyunCLI, RegionID: config.RegionID, VSwitchID: config.VSwitchID,
		SecurityGroupID: config.SecurityGroupID, WorkerRoleName: config.WorkerRoleName,
		Profile: config.CredentialProfile, Deadline: 30 * time.Minute,
		SpotStrategy: eci.SpotStrategyNoSpot,
	})
}

// initializeRemoteGenerationOneProvision 通过 gate 唯一入口执行空表原子 INSERT。
func initializeRemoteGenerationOneProvision(options remoteGenerationOneProvisionOptions, input remoteGenerationOneProvisionInput) (gatecontract.RemoteBaselineStateRecord, error) {
	store, err := baselineLedger(options.LedgerPath)
	if err != nil {
		return gatecontract.RemoteBaselineStateRecord{}, infrastructureError("open generation-one SQLite authority: %v", err)
	}
	record, err := store.InitializeRemoteBaselineGenerationOne(input.ReceiptJSON)
	if err != nil {
		return gatecontract.RemoteBaselineStateRecord{}, protocolError("initialize remote baseline generation one: %v", err)
	}
	return record, nil
}

// writeRemoteGenerationOneProvisionResult 输出不含云凭据的严格导入结果。
func writeRemoteGenerationOneProvisionResult(stdout io.Writer, receipt cicontract.GenerationOneProvisionReceipt, record gatecontract.RemoteBaselineStateRecord) error {
	result := remoteGenerationOneProvisionResult{
		SchemaVersion: cicontract.GenerationOneProvisionReceiptSchemaVersion,
		Authority:     cicontract.GenerationOneProvisionAuthority, Generation: record.Generation,
		StateSHA256: "sha256:" + record.StateSHA256, ReceiptSHA256: receipt.ReceiptSHA256,
		ImageCacheID: receipt.ImageCacheID, ImageCacheSnapshotID: receipt.ImageCacheSnapshotID,
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		return infrastructureError("write generation-one provision result: %v", err)
	}
	return nil
}

// readGenerationOneProvisionReceipt 只读取有界普通文件，拒绝目录、空文件和超大输入。
func readGenerationOneProvisionReceipt(path string) ([]byte, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > remoteGenerationOneProvisionReceiptLimit {
		return nil, errors.New("generation-one provision receipt must be a bounded regular file")
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// decodeGenerationOneProvisionState 严格解码 canonical BaselineState 并执行其完整校验。
func decodeGenerationOneProvisionState(receipt cicontract.GenerationOneProvisionReceipt) (remoteci.BaselineState, error) {
	var state remoteci.BaselineState
	if err := gatecontract.DecodeStrictJSON(receipt.StateJSON, &state); err != nil {
		return remoteci.BaselineState{}, err
	}
	canonical, err := json.Marshal(state)
	if err != nil {
		return remoteci.BaselineState{}, err
	}
	if !bytes.Equal(canonical, receipt.StateJSON) {
		return remoteci.BaselineState{}, errors.New("generation-one state_json is not canonical JSON")
	}
	if err := state.Validate(); err != nil {
		return remoteci.BaselineState{}, err
	}
	return state, nil
}

// validateGenerationOneProvisionStateBinding 证明回执字段与 canonical BaselineState 一一绑定。
func validateGenerationOneProvisionStateBinding(receipt cicontract.GenerationOneProvisionReceipt, state remoteci.BaselineState) error {
	if err := validateGenerationOneStateCoreBinding(receipt, state); err != nil {
		return err
	}
	if err := validateGenerationOneStateArtifactBinding(receipt, state); err != nil {
		return err
	}
	return nil
}

// validateGenerationOneStateCoreBinding 校验代次、缓存、平台与策略身份。
func validateGenerationOneStateCoreBinding(receipt cicontract.GenerationOneProvisionReceipt, state remoteci.BaselineState) error {
	if state.Generation != 1 || state.ImageCacheID != receipt.ImageCacheID || state.ImageCacheSnapshotID != receipt.ImageCacheSnapshotID || state.RuntimeImage != receipt.RuntimeImage || state.MainCommit != receipt.MainCommit || state.MainTree != receipt.MainTree || state.Platform != receipt.Platform || state.PolicyDigest != receipt.PolicyDigest || state.ToolchainDigest != receipt.ToolchainDigest {
		return errors.New("generation-one receipt core fields do not match BaselineState")
	}
	return nil
}

// validateGenerationOneStateArtifactBinding 校验 Gate、seed 与 baseline manifest 摘要。
func validateGenerationOneStateArtifactBinding(receipt cicontract.GenerationOneProvisionReceipt, state remoteci.BaselineState) error {
	if state.GateBinarySHA256 != receipt.GateBinarySHA256 || state.RuntimeSeedSHA256 != receipt.RuntimeSeedSHA256 || state.BaselineManifestDigest != receipt.BaselineManifestDigest {
		return errors.New("generation-one receipt artifact fields do not match BaselineState")
	}
	return nil
}
