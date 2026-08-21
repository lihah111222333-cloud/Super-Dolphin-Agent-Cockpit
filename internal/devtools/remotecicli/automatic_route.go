package remotecicli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

const automaticRouteConfigSchema = "automatic-heteroci-route/v1"

// AutomaticRouteConfig 是自动仓库识别、动态探针和 local-first 路由所需的显式配置。
type AutomaticRouteConfig struct {
	SchemaVersion        string               `json:"schema_version"`
	TopologyManifestPath string               `json:"topology_manifest_path"`
	OwnerDBPath          string               `json:"owner_db_path"`
	ReceiptDBPath        string               `json:"receipt_db_path"`
	OwnerNamespace       string               `json:"owner_namespace"`
	SSHExecutable        string               `json:"ssh_executable"`
	ProbeTimeoutMS       int64                `json:"probe_timeout_ms"`
	ProbeWaitDelayMS     int64                `json:"probe_wait_delay_ms"`
	HeartbeatTTLMS       int64                `json:"heartbeat_ttl_ms"`
	RouteTimeoutMS       int64                `json:"route_timeout_ms"`
	CleanupTimeoutMS     int64                `json:"cleanup_timeout_ms"`
	Nodes                []AutomaticProbeNode `json:"nodes"`
}

// AutomaticProbeNode 把 manifest node 绑定到唯一 probe binary 与 config 路径。
type AutomaticProbeNode struct {
	NodeID             string `json:"node_id"`
	ProbeExecutable    string `json:"probe_executable"`
	ProbeConfigPath    string `json:"probe_config_path"`
	RunnerExecutable   string `json:"runner_executable"`
	WorkerExecutable   string `json:"worker_executable"`
	WorkerIdentity     string `json:"worker_identity"`
	RepositoryBasePath string `json:"repository_base_path"`
	CacheRoot          string `json:"cache_root"`
	GoExecutable       string `json:"go_executable"`
	ToolchainIdentity  string `json:"toolchain_identity"`
	SharedRoot         string `json:"shared_root"`
	DockerImage        string `json:"docker_image"`
	RunAsUser          string `json:"run_as_user"`
	DockerGroup        string `json:"docker_group"`
}

type automaticRouteInputs struct {
	config     AutomaticRouteConfig
	repository DetectedRepository
	manifest   heteroci.Manifest
	commands   map[string][]string
	ownerScope heteroci.RoutingOwnerScope
	ctx        context.Context
	cancel     context.CancelFunc
}

// prepareAutomaticRouteInputs 绑定 strict config、exact repository manifest 与动态 topology。
func prepareAutomaticRouteInputs(ctx context.Context, configPath, repositoryPath string, stderr io.Writer) (inputs automaticRouteInputs, returnErr error) {
	config, err := loadAutomaticRouteConfig(configPath)
	if err != nil {
		return automaticRouteInputs{}, newCLIFailure("load automatic route config", err)
	}
	routeCtx, cancel := context.WithTimeout(ctx, time.Duration(config.RouteTimeoutMS)*time.Millisecond)
	defer func() {
		if returnErr != nil {
			cancel()
		}
	}()
	repository, err := DetectRepository(routeCtx, repositoryPath)
	if err != nil {
		return automaticRouteInputs{}, newCLIFailure("detect target repository", err)
	}
	repository, err = expandDetectedRepositoryGoCatalog(routeCtx, repository)
	if err != nil {
		return automaticRouteInputs{}, newCLIFailure("derive exact-tree Go workload catalog", err)
	}
	if err := writeProgress(stderr, "target repository detected"); err != nil {
		return automaticRouteInputs{}, newCLIFailure("write progress", err)
	}
	manifest, err := heteroci.LoadManifestFile(config.TopologyManifestPath)
	if err != nil {
		return automaticRouteInputs{}, newCLIFailure("load topology manifest", err)
	}
	commands, err := bindAutomaticProbeCommands(config, manifest)
	if err != nil {
		return automaticRouteInputs{}, err
	}
	ownerScope, err := deriveAutomaticRouteOwnerScope(config.OwnerNamespace, repository, manifest)
	if err != nil {
		return automaticRouteInputs{}, err
	}
	return automaticRouteInputs{config: config, repository: repository, manifest: manifest, commands: commands, ownerScope: ownerScope, ctx: routeCtx, cancel: cancel}, nil
}

// deriveAutomaticRouteOwnerScope 按 namespace、仓库 exact tree 与 topology 派生隔离 scope。
func deriveAutomaticRouteOwnerScope(namespace string, repository DetectedRepository, manifest heteroci.Manifest) (heteroci.RoutingOwnerScope, error) {
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return "", errors.New("automatic route topology identity cannot be encoded")
	}
	material := strings.Join([]string{"automatic-route-owner-scope/v1", namespace, repository.RepositoryID, repository.TreeSHA, digestBytes(encoded)}, "\x00")
	digest := sha256.Sum256([]byte(material))
	return heteroci.ParseRoutingOwnerScope("sha256:" + hex.EncodeToString(digest[:]))
}

// loadAutomaticRouteConfig 严格解码并校验自动路由配置。
func loadAutomaticRouteConfig(configPath string) (AutomaticRouteConfig, error) {
	raw, err := readDescriptorFile(configPath)
	if err != nil {
		return AutomaticRouteConfig{}, err
	}
	if err := rejectRuntimeJSONDuplicates(raw); err != nil {
		return AutomaticRouteConfig{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var config AutomaticRouteConfig
	if err := decoder.Decode(&config); err != nil {
		return AutomaticRouteConfig{}, errors.New("automatic route config JSON is invalid")
	}
	if err := ensureAutomaticRouteConfigEOF(decoder); err != nil {
		return AutomaticRouteConfig{}, err
	}
	if err := validateAutomaticRouteConfig(config); err != nil {
		return AutomaticRouteConfig{}, err
	}
	canonical, err := json.Marshal(config)
	if err != nil || !bytes.Equal(canonical, raw) {
		return AutomaticRouteConfig{}, errors.New("automatic route config is not canonical")
	}
	return config, nil
}

// ensureAutomaticRouteConfigEOF 拒绝尾随 JSON 值。
func ensureAutomaticRouteConfigEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("automatic route config has trailing JSON")
	}
	return nil
}

// validateAutomaticRouteConfig 校验 schema、路径、时限和节点全集。
func validateAutomaticRouteConfig(config AutomaticRouteConfig) error {
	if config.SchemaVersion != automaticRouteConfigSchema {
		return errors.New("automatic route config schema_version is invalid")
	}
	if err := validateAutomaticRouteHostPaths(config); err != nil {
		return err
	}
	if !validAutomaticIdentity(config.OwnerNamespace) {
		return errors.New("automatic route owner_namespace is invalid")
	}
	if err := validateAutomaticRouteDurations(config); err != nil {
		return err
	}
	return validateAutomaticRouteNodes(config.Nodes)
}

// validateAutomaticRouteDurations 校验所有 probe 与 heartbeat 时间边界。
func validateAutomaticRouteDurations(config AutomaticRouteConfig) error {
	if config.ProbeTimeoutMS < 1_000 || config.ProbeTimeoutMS > 120_000 || config.ProbeWaitDelayMS <= 0 || config.ProbeWaitDelayMS > 10_000 {
		return errors.New("automatic route probe timeout is invalid")
	}
	if config.HeartbeatTTLMS <= 0 || config.HeartbeatTTLMS > 120_000 {
		return errors.New("automatic route heartbeat TTL is invalid")
	}
	return validateAutomaticRouteOverallDurations(config)
}

// validateAutomaticRouteOverallDurations 校验 route 与 cleanup 总体边界。
func validateAutomaticRouteOverallDurations(config AutomaticRouteConfig) error {
	if config.RouteTimeoutMS < 1_000 || config.RouteTimeoutMS > 600_000 {
		return errors.New("automatic route overall timeout is invalid")
	}
	if config.CleanupTimeoutMS < 1_000 || config.CleanupTimeoutMS > 120_000 {
		return errors.New("automatic route cleanup timeout is invalid")
	}
	return nil
}

// validateAutomaticRouteNodes 校验 node bindings 非空、严格排序且唯一。
func validateAutomaticRouteNodes(nodes []AutomaticProbeNode) error {
	if len(nodes) == 0 {
		return errors.New("automatic route node bindings are required")
	}
	for index, node := range nodes {
		if err := validateAutomaticProbeNode(node); err != nil {
			return fmt.Errorf("automatic route node %d: %w", index, err)
		}
		if index > 0 && nodes[index-1].NodeID >= node.NodeID {
			return errors.New("automatic route nodes must be sorted and unique")
		}
	}
	return nil
}

// validateAutomaticRouteHostPaths 校验所有 host 路径显式、绝对且无 symlink。
func validateAutomaticRouteHostPaths(config AutomaticRouteConfig) error {
	for _, candidate := range []struct {
		path      string
		directory bool
	}{
		{path: config.TopologyManifestPath}, {path: config.OwnerDBPath}, {path: config.ReceiptDBPath}, {path: config.SSHExecutable},
	} {
		if err := validateAbsolutePathComponents(candidate.path, candidate.directory); err != nil {
			return errors.New("automatic route host path is invalid")
		}
	}
	return nil
}

// validateAutomaticProbeNode 校验 node identity 与目标平台绝对路径形状。
func validateAutomaticProbeNode(node AutomaticProbeNode) error {
	if !validAutomaticIdentity(node.NodeID) {
		return errors.New("node_id is invalid")
	}
	if !validRemoteAbsolutePath(node.ProbeExecutable) || !validRemoteAbsolutePath(node.ProbeConfigPath) {
		return errors.New("probe executable and config paths must be absolute")
	}
	for _, value := range []string{node.RunnerExecutable, node.WorkerExecutable, node.RepositoryBasePath, node.CacheRoot} {
		if !validRemoteAbsolutePath(value) {
			return errors.New("runner, worker, repository, and cache paths must be absolute")
		}
	}
	if !validAutomaticIdentity(node.GoExecutable) || !isDigest(node.WorkerIdentity) || !isDigest(node.ToolchainIdentity) {
		return errors.New("worker and toolchain identities are invalid")
	}
	return nil
}

// validAutomaticIdentity 拒绝空白、控制字符和不稳定身份。
func validAutomaticIdentity(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || len(value) > 256 {
		return false
	}
	return strings.IndexFunc(value, unicode.IsControl) < 0
}

// validRemoteAbsolutePath 同时接受 Unix 与 Windows 的显式绝对路径。
func validRemoteAbsolutePath(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return false
	}
	if path.IsAbs(value) {
		return path.Clean(value) == value
	}
	return validWindowsRemoteAbsolutePath(value)
}

// validWindowsRemoteAbsolutePath 校验显式盘符开头的 Windows 路径。
func validWindowsRemoteAbsolutePath(value string) bool {
	if len(value) < 3 || value[1] != ':' || value[2] != '\\' && value[2] != '/' {
		return false
	}
	return value[0] >= 'A' && value[0] <= 'Z' || value[0] >= 'a' && value[0] <= 'z'
}

// bindAutomaticProbeCommands 对 manifest 节点全集做双向差集并生成固定 probe argv。
func bindAutomaticProbeCommands(config AutomaticRouteConfig, manifest heteroci.Manifest) (map[string][]string, error) {
	bindings := make(map[string]AutomaticProbeNode, len(config.Nodes))
	for _, binding := range config.Nodes {
		bindings[binding.NodeID] = binding
	}
	commands := make(map[string][]string, len(manifest.Nodes))
	for _, node := range manifest.Nodes {
		binding, ok := bindings[node.NodeID]
		if !ok {
			return nil, errors.New("automatic route config is missing a manifest node")
		}
		if err := validateAutomaticNodePlatformFields(node.Platform, binding); err != nil {
			return nil, err
		}
		commands[node.NodeID] = []string{binding.ProbeExecutable, "probe", "--config", binding.ProbeConfigPath}
		delete(bindings, node.NodeID)
	}
	if len(bindings) != 0 {
		stale := make([]string, 0, len(bindings))
		for nodeID := range bindings {
			stale = append(stale, nodeID)
		}
		slices.Sort(stale)
		return nil, errors.New("automatic route config contains a stale node binding")
	}
	return commands, nil
}

// validateAutomaticNodePlatformFields 拒绝 Linux/Windows 执行字段混用。
func validateAutomaticNodePlatformFields(platform heteroci.Platform, binding AutomaticProbeNode) error {
	switch platform {
	case heteroci.PlatformLinuxAMD64:
		return validateAutomaticLinuxNodeFields(binding)
	case heteroci.PlatformWindowsAMD64:
		return validateAutomaticWindowsNodeFields(binding)
	default:
		return errors.New("automatic node platform fields are invalid")
	}
}

func validateAutomaticLinuxNodeFields(binding AutomaticProbeNode) error {
	if !validAutomaticIdentity(binding.RunAsUser) || !validAutomaticIdentity(binding.DockerGroup) || !validRemoteAbsolutePath(binding.SharedRoot) || binding.DockerImage == "" {
		return errors.New("automatic Linux node fields are invalid")
	}
	return nil
}

func validateAutomaticWindowsNodeFields(binding AutomaticProbeNode) error {
	if binding.RunAsUser != "" || binding.DockerGroup != "" || binding.SharedRoot != "" || binding.DockerImage != "" {
		return errors.New("automatic Windows node fields are invalid")
	}
	return nil
}
