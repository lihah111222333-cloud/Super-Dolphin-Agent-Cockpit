package main

import (
	"errors"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteciruntime"
)

const (
	remoteContainerReportAllowance = 30 * time.Second
)

const remoteCalibrationResultSchemaVersion uint32 = 4

var errRemoteCalibrationSamplesIncomplete = errors.New("remote calibration samples are incomplete")

type remoteRunConfig = remoteciruntime.Config

type remoteRunOptions struct {
	Target                string
	ConfigPath            string
	RepositoryRoot        string
	RemoteName            string
	RemoteURL             string
	Commit                string
	Tree                  string
	ParentCommit          string
	Base                  string
	Scenario              string
	Entrypoint            string
	Tests                 []string
	WorkloadID            string
	GateWorkloadIDs       []string
	GateWorkloadManifest  string
	CompletionReceiptPath string
	LocalRef              string
	RemoteRef             string
	ObservedRemote        string
	UpdateKind            string
	LedgerPath            string
	AgentTokenDigest      string
	Calibration           bool
	Force                 bool
	ProgressObserver      remoteci.ProgressObserver
}

type remoteStringListFlag []string

// String 将重复测试选择器编码为 flag.Value 可显示的逗号列表。
func (values *remoteStringListFlag) String() string { return strings.Join(*values, ",") }

// Set 拒绝空测试选择器并保留调用顺序。
func (values *remoteStringListFlag) Set(value string) error {
	if value == "" {
		return errors.New("test selector must not be empty")
	}
	*values = append(*values, value)
	return nil
}

type remoteCalibrationResult struct {
	SchemaVersion                uint32    `json:"schema_version"`
	Force                        bool      `json:"force"`
	Commit                       string    `json:"commit"`
	Tree                         string    `json:"tree"`
	RunnerManifestDigest         string    `json:"runner_manifest_digest"`
	CalibrationResourceClassID   string    `json:"calibration_resource_class_id"`
	CalibrationResourceCPU       float64   `json:"calibration_resource_cpu"`
	CalibrationResourceMemoryGiB float64   `json:"calibration_resource_memory_gib"`
	CommitJobID                  string    `json:"commit_job_id"`
	PushJobID                    string    `json:"push_job_id"`
	ReleaseJobID                 string    `json:"release_job_id"`
	LedgerGeneration             uint64    `json:"ledger_generation"`
	WorkloadCount                int       `json:"workload_count"`
	RacePackageCount             int       `json:"race_package_count"`
	CompletedAt                  time.Time `json:"completed_at"`
}
