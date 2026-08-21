package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterocinode"
)

const nodeConfigMaxBytes = 1 << 20

type nodeRuntimeConfig struct {
	SchemaVersion string                    `json:"schema_version"`
	Node          heteroci.NodeConfig       `json:"node"`
	Linux         *nodeLinuxRuntimeConfig   `json:"linux"`
	Windows       *nodeWindowsRuntimeConfig `json:"windows"`
}

type nodeLinuxRuntimeConfig struct {
	DockerExecutable string `json:"docker_executable"`
	CgroupRoot       string `json:"cgroup_root"`
	BootIDPath       string `json:"boot_id_path"`
	TimeoutMS        int64  `json:"timeout_ms"`
	WaitDelayMS      int64  `json:"wait_delay_ms"`
}

type nodeWindowsRuntimeConfig struct {
	PowerShellExecutable string `json:"powershell_executable"`
	TimeoutMS            int64  `json:"timeout_ms"`
	WaitDelayMS          int64  `json:"wait_delay_ms"`
}

type nodeProviderFactory func(nodeRuntimeConfig) (heterocinode.FactsProvider, error)

// runNodeCLI 保持 stdout 为唯一 response 协议，并把 unavailable 固定映射为 SSH exit 255。
func runNodeCLI(args []string, stdout, stderr io.Writer, factory nodeProviderFactory) int {
	if len(args) == 0 {
		return writeNodeError(stderr, "probe or batch command is required", 2)
	}
	if args[0] == "batch" {
		return runNodeBatchCLI(args[1:], stdout, stderr)
	}
	if args[0] != "probe" || factory == nil {
		return writeNodeError(stderr, "probe command is required", 2)
	}
	configPath, requestToken, err := parseNodeProbeArgs(args[1:])
	if err != nil {
		return writeNodeError(stderr, "probe arguments are invalid", 2)
	}
	response, unavailable, errorCode := executeNodeProbe(configPath, requestToken, factory)
	if unavailable {
		return writeNodeError(stderr, "probe unavailable error_code="+errorCode, 255)
	}
	if errorCode != "" {
		return writeNodeError(stderr, "probe failed error_code="+errorCode, 2)
	}
	if _, err := stdout.Write(response); err != nil {
		return writeNodeError(stderr, "probe output failed", 3)
	}
	return 0
}

// runNodeBatchCLI 严格解码 base64 request，并让 worker 输出唯一 canonical batch result。
func runNodeBatchCLI(args []string, stdout, stderr io.Writer) int {
	requestToken, err := parseNodeBatchArgs(args)
	if err != nil {
		return writeNodeError(stderr, "batch arguments are invalid", 2)
	}
	requestBytes, err := decodeNodeBatchToken(requestToken)
	if err != nil {
		return writeNodeError(stderr, "batch request is invalid", 2)
	}
	request, err := heterocinode.DecodeBatchRequest(requestBytes)
	if err != nil {
		return writeNodeError(stderr, "batch request is invalid", 2)
	}
	result, err := heterocinode.RunBatch(context.Background(), request, stdout, stderr)
	if err != nil {
		return writeNodeError(stderr, "batch failed error_code=worker_invalid", 2)
	}
	if result.Outcome != heterocinode.BatchOutcomeSuccess {
		return 1
	}
	return 0
}

// decodeNodeBatchToken 校验并解码 canonical base64 batch request。
func decodeNodeBatchToken(token string) ([]byte, error) {
	if token == "" || strings.TrimSpace(token) != token || len(token) > 2<<20 {
		return nil, errors.New("batch request token is invalid")
	}
	decoded, err := base64.StdEncoding.DecodeString(token)
	if err != nil || len(decoded) == 0 || len(decoded) > 1<<20 || base64.StdEncoding.EncodeToString(decoded) != token {
		return nil, errors.New("batch request token is not canonical")
	}
	return decoded, nil
}

// parseNodeBatchArgs 只接受一次 --request-base64 且拒绝位置参数。
func parseNodeBatchArgs(args []string) (string, error) {
	requestFlagCount := 0
	for _, arg := range args {
		if arg == "--request-base64" || strings.HasPrefix(arg, "--request-base64=") {
			requestFlagCount++
		}
	}
	if requestFlagCount != 1 {
		return "", errors.New("node batch request-base64 flag must be provided exactly once")
	}
	flags := flag.NewFlagSet("heteroci-node batch", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var request string
	flags.StringVar(&request, "request-base64", "", "canonical batch request")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || request == "" {
		return "", errors.New("node batch arguments are invalid")
	}
	return request, nil
}

// executeNodeProbe 以稳定 error code 区分配置、请求、provider、facts 与 response 失败。
func executeNodeProbe(configPath, requestToken string, factory nodeProviderFactory) ([]byte, bool, string) {
	config, err := loadNodeRuntimeConfig(configPath)
	if err != nil {
		return nil, false, "config_invalid"
	}
	request, err := decodeNodeProbeToken(requestToken)
	if err != nil {
		return nil, false, "request_invalid"
	}
	provider, err := factory(config)
	if err != nil {
		return nil, true, "provider_unavailable"
	}
	coreConfig := heterocinode.Config{SchemaVersion: config.SchemaVersion, Node: config.Node}
	response, err := heterocinode.Probe(context.Background(), coreConfig, request, provider, time.Now())
	if errors.Is(err, heterocinode.ErrFactsUnavailable) {
		return nil, true, "facts_unavailable"
	}
	if errors.Is(err, heterocinode.ErrFactsInvalid) {
		return nil, false, "facts_invalid"
	}
	if errors.Is(err, heterocinode.ErrResponseInvalid) {
		return nil, false, "response_invalid"
	}
	if err != nil {
		return nil, false, "probe_invalid"
	}
	return response, false, ""
}

func parseNodeProbeArgs(args []string) (string, string, error) {
	flags := flag.NewFlagSet("heteroci-node probe", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var configPath, request string
	flags.StringVar(&configPath, "config", "", "absolute node config path")
	flags.StringVar(&request, "probe-request-base64", "", "canonical probe request")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || configPath == "" || request == "" {
		return "", "", errors.New("node probe arguments are invalid")
	}
	return configPath, request, nil
}

// loadNodeRuntimeConfig 严格读取 canonical、无 symlink 的 node runtime config。
func loadNodeRuntimeConfig(path string) (nodeRuntimeConfig, error) {
	if err := validateNodeConfigPath(path); err != nil {
		return nodeRuntimeConfig{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 || len(data) > nodeConfigMaxBytes {
		return nodeRuntimeConfig{}, errors.New("node config read failed")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var config nodeRuntimeConfig
	if err := decoder.Decode(&config); err != nil || !nodeJSONEOF(decoder) {
		return nodeRuntimeConfig{}, errors.New("node config JSON is invalid")
	}
	canonical, err := json.Marshal(config)
	if err != nil || !bytes.Equal(canonical, data) || validateNodeRuntimeConfig(config) != nil {
		return nodeRuntimeConfig{}, errors.New("node config is not canonical")
	}
	return config, nil
}

// validateNodeRuntimeConfig 校验 core node 与 platform provider one-of。
func validateNodeRuntimeConfig(config nodeRuntimeConfig) error {
	if err := (heterocinode.Config{SchemaVersion: config.SchemaVersion, Node: config.Node}).Validate(); err != nil {
		return err
	}
	switch config.Node.Platform {
	case heteroci.PlatformLinuxAMD64:
		if config.Linux == nil || config.Windows != nil {
			return errors.New("linux node config one-of is invalid")
		}
	case heteroci.PlatformWindowsAMD64:
		if config.Windows == nil || config.Linux != nil {
			return errors.New("windows node config one-of is invalid")
		}
	default:
		return errors.New("node platform is unsupported")
	}
	return nil
}

// validateNodeConfigPath 拒绝相对、非 clean、symlink 或非普通配置文件。
func validateNodeConfigPath(path string) error {
	clean := filepath.Clean(path)
	if path == "" || !filepath.IsAbs(path) || path != clean && path != filepath.ToSlash(clean) {
		return errors.New("node config path is invalid")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || filepath.Clean(resolved) != clean {
		return errors.New("node config path contains a symlink")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("node config must be a regular file")
	}
	return nil
}

// decodeNodeProbeToken 严格解码并回编码 caller 传入的 canonical base64 request。
func decodeNodeProbeToken(token string) ([]byte, error) {
	if token == "" || strings.TrimSpace(token) != token || len(token) > 8<<10 {
		return nil, errors.New("probe request token is invalid")
	}
	decoded, err := base64.StdEncoding.DecodeString(token)
	if err != nil || base64.StdEncoding.EncodeToString(decoded) != token {
		return nil, errors.New("probe request token is not canonical")
	}
	return decoded, nil
}

func nodeJSONEOF(decoder *json.Decoder) bool {
	var trailing json.RawMessage
	return errors.Is(decoder.Decode(&trailing), io.EOF)
}

func defaultNodeProviderFactory(config nodeRuntimeConfig) (heterocinode.FactsProvider, error) {
	switch config.Node.Platform {
	case heteroci.PlatformLinuxAMD64:
		return heterocinode.NewLinuxFactsProvider(heterocinode.LinuxFactsProviderConfig{
			DockerExecutable: config.Linux.DockerExecutable,
			CgroupRoot:       config.Linux.CgroupRoot, BootIDPath: config.Linux.BootIDPath,
			Timeout:   time.Duration(config.Linux.TimeoutMS) * time.Millisecond,
			WaitDelay: time.Duration(config.Linux.WaitDelayMS) * time.Millisecond,
		})
	case heteroci.PlatformWindowsAMD64:
		return heterocinode.NewWindowsFactsProvider(heterocinode.WindowsFactsProviderConfig{
			PowerShellExecutable: config.Windows.PowerShellExecutable,
			Timeout:              time.Duration(config.Windows.TimeoutMS) * time.Millisecond,
			WaitDelay:            time.Duration(config.Windows.WaitDelayMS) * time.Millisecond,
		})
	default:
		return nil, errors.New("node platform provider is unavailable")
	}
}

func writeNodeError(stderr io.Writer, message string, code int) int {
	if _, err := io.WriteString(stderr, "super-dolphin-heteroci-node: "+message+"\n"); err != nil {
		return 3
	}
	return code
}
