package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

// createRuntimeLSPClient 为一次已解析 workspace 构造独立 client，并注入共享资源预算环境。
func createRuntimeLSPClient(
	adapter multilsp.LanguageAdapter,
	command multilsp.ServerCommand,
	root string,
	packagedLSP bool,
	binary *runtimeBinaryOverride,
	rootDir string,
	env []string,
	initOptions map[string]any,
	goplsRootController multilsp.GoplsRootCohortController,
	handler protocol.NotificationHandler,
) (multilsp.Client, error) {
	dir := runtimeServerLSPClientDir(root, rootDir)
	serverBinary := binary.Get()
	serverBinary, err := runtimeServerTrustedGoplsClientBinary(command, serverBinary)
	if err != nil {
		return nil, err
	}
	goplsLease, err := runtimeServerAcquireGoplsRootLease(command, serverBinary, dir, env, goplsRootController)
	if err != nil {
		return nil, err
	}
	preparation, err := runtimeServerPrepareLSPClient(adapter, command, serverBinary, dir, env, initOptions, packagedLSP, handler)
	if err != nil {
		return nil, errors.Join(err, runtimeServerReleaseLSPClientResources(preparation.serverEnv, goplsLease))
	}
	processBinary, err := runtimeServerPlatformProcessBinary(serverBinary)
	if err != nil {
		return nil, errors.Join(err, runtimeServerReleaseLSPClientResources(preparation.serverEnv, goplsLease))
	}
	var vueBridge *runtimeVueTSBridgeClient
	if preparation.vueBridgeSpec != nil {
		vueBridge, err = runtimeServerStartVueTSBridge(*preparation.vueBridgeSpec, dir, preparation.serverEnv)
		if err != nil {
			return nil, errors.Join(err, runtimeServerReleaseLSPClientResources(preparation.serverEnv, goplsLease))
		}
	}
	markdownSupport, err := runtimeMarkdownClientSupportForAdapter(adapter, dir, preparation.serverEnv, serverBinary)
	if err != nil {
		var bridgeErr error
		if vueBridge != nil {
			bridgeErr = vueBridge.Close()
		}
		return nil, errors.Join(err, bridgeErr, runtimeServerReleaseLSPClientResources(preparation.serverEnv, goplsLease))
	}
	serverNotificationHandler := multilsp.ServerNotificationHandler(nil)
	if vueBridge != nil {
		serverNotificationHandler = vueBridge.handleServerNotification
	}
	if markdownSupport != nil {
		serverNotificationHandler = markdownSupport.ServerNotificationHandler()
	}
	var serverRequestHandler multilsp.ServerRequestHandler
	if markdownSupport != nil {
		serverRequestHandler = markdownSupport.RequestHandler()
	}
	client, err := multilsp.NewClientWithOptions(multilsp.Options{
		Binary:                    processBinary,
		Args:                      preparation.serverArgs,
		Dir:                       dir,
		Env:                       preparation.serverEnv,
		InitOptions:               preparation.initOptions,
		NotificationHandler:       preparation.notificationHandler,
		RequestHandler:            serverRequestHandler,
		ServerNotificationHandler: serverNotificationHandler,
		ServerProfile:             preparation.serverProfile,
	})
	if err != nil {
		var bridgeErr error
		if vueBridge != nil {
			bridgeErr = vueBridge.Close()
		}
		var markdownErr error
		if markdownSupport != nil {
			markdownErr = markdownSupport.Close()
		}
		return nil, errors.Join(err, bridgeErr, markdownErr, runtimeServerReleaseLSPClientResources(preparation.serverEnv, goplsLease))
	}
	if markdownSupport != nil {
		markdownSupport.Attach(client)
		client = wrapRuntimeMarkdownClient(client, markdownSupport)
	}
	if vueBridge != nil {
		vueBridge.vue = client
		client = vueBridge
	}
	return runtimeServerFinalizeLSPClient(client, preparation, dir, handler, goplsLease)
}

// runtimeServerLSPClientPreparation 保存 client 创建阶段的 args、env 与 wrapper 决策。
type runtimeServerLSPClientPreparation struct {
	serverArgs          []string
	serverEnv           []string
	initOptions         map[string]any
	notificationHandler protocol.NotificationHandler
	vueBridgeSpec       *runtimeVueTSBridgeSpec
	sqliteWorkspace     bool
	serverProfile       string
}

// runtimeServerLSPClientDir 解析 client 的最终工作目录，空 rootDir 沿用 root。
func runtimeServerLSPClientDir(root, rootDir string) string {
	dir := strings.TrimSpace(rootDir)
	if dir == "" {
		return root
	}
	return dir
}

// runtimeServerPrepareLSPClient 计算 server args/env/init options，并建立 SQL 通知包装。
func runtimeServerPrepareLSPClient(adapter multilsp.LanguageAdapter, command multilsp.ServerCommand, serverBinary, dir string, env []string, initOptions map[string]any, packagedLSP bool, handler protocol.NotificationHandler) (runtimeServerLSPClientPreparation, error) {
	preparation := runtimeServerLSPClientPreparation{}
	serverArgs, err := runtimeServerArgsPlatform(command, serverBinary, env, dir)
	if err != nil {
		return preparation, err
	}
	serverArgs, preparation.vueBridgeSpec, err = runtimeServerPrepareVueBridge(adapter, serverBinary, serverArgs)
	if err != nil {
		return preparation, err
	}
	preparation.serverArgs = serverArgs
	resourceCommand := command
	resourceCommand.Args = serverArgs
	preparation.serverEnv, err = runtimeServerEnvironment(resourceCommand, serverBinary, dir, adapter.LanguageIDs(), env, runtimeAdapterUsesNode(adapter))
	if err != nil {
		return preparation, err
	}
	preparation.sqliteWorkspace, err = runtimeSQLiteDiagnosticsWorkspace(adapter, dir)
	if err != nil {
		return preparation, err
	}
	preparation.notificationHandler = handler
	if preparation.sqliteWorkspace {
		preparation.notificationHandler = &sqlDiagnosticNotificationHandler{root: dir, next: handler}
	}
	preparation.initOptions, err = runtimeServerInitOptions(adapter, initOptions, packagedLSP, serverBinary, preparation.serverEnv)
	preparation.serverProfile = runtimeServerProductProfile(adapter, command, serverBinary, preparation.serverArgs)
	return preparation, err
}

// runtimeServerReleaseLSPClientResources 释放 resource cohort 与 gopls root lease。
func runtimeServerReleaseLSPClientResources(serverEnv []string, goplsLease *multilsp.GoplsRootCohortLease) error {
	if len(serverEnv) == 0 {
		return releaseGoplsRootLease(goplsLease)
	}
	return errors.Join(multilsp.ReleaseResourceCohortLease(serverEnv), releaseGoplsRootLease(goplsLease))
}

// runtimeServerFinalizeLSPClient 应用 SQL 或 gopls wrapper，并保持两者互斥。
func runtimeServerFinalizeLSPClient(client multilsp.Client, preparation runtimeServerLSPClientPreparation, dir string, handler protocol.NotificationHandler, goplsLease *multilsp.GoplsRootCohortLease) (multilsp.Client, error) {
	if !preparation.sqliteWorkspace {
		if goplsLease == nil {
			return client, nil
		}
		return &goplsRootCohortClient{Client: client, lease: goplsLease}, nil
	}
	if goplsLease != nil {
		return nil, errors.Join(client.Close(), releaseGoplsRootLease(goplsLease), errors.New("gopls root cohort cannot use SQL diagnostic wrapper"))
	}
	return newSQLDiagnosticClient(client, dir, handler)
}

func releaseGoplsRootLease(lease *multilsp.GoplsRootCohortLease) error {
	if lease == nil {
		return nil
	}
	return lease.Release()
}

// goplsRootCohortClient 把 root cohort lease 的 release 绑定到真实 LSP client Close。
// 这样 manager cleanup 与 controller fence 使用同一 owner，不会提前释放 active member。
type goplsRootCohortClient struct {
	multilsp.Client
	lease *multilsp.GoplsRootCohortLease
}

var (
	_ multilsp.IdleReleasableClient      = (*goplsRootCohortClient)(nil)
	_ multilsp.IdleReleaseRequiredClient = (*goplsRootCohortClient)(nil)
	_ multilsp.HealthCheckedClient       = (*goplsRootCohortClient)(nil)
)

// UnderlyingLSPClient 保留真实 transport owner，供 multilsp 进程树和资源清理穿透 wrapper。
func (c *goplsRootCohortClient) UnderlyingLSPClient() multilsp.Client {
	if c == nil {
		return nil
	}
	return c.Client
}

// ServerCapabilities 保留真实 gopls capability 面，避免 lease wrapper 改变能力判断。
func (c *goplsRootCohortClient) ServerCapabilities() protocol.ServerCapabilities {
	if c == nil || c.Client == nil {
		return protocol.ServerCapabilities{}
	}
	capabilities, ok := c.Client.(multilsp.ServerCapabilitiesClient)
	if !ok {
		return protocol.ServerCapabilities{}
	}
	return capabilities.ServerCapabilities()
}

// Healthy 穿透 lease wrapper 返回真实 forwarder transport 健康状态，确保 daemon
// 断开后 manager 能摘除旧 generation 并创建 replacement client。
func (c *goplsRootCohortClient) Healthy() bool {
	if c == nil || c.Client == nil {
		return false
	}
	health, ok := c.Client.(multilsp.HealthCheckedClient)
	return ok && health.Healthy()
}

// Close 按平台顺序关闭 forwarder 并释放 root cohort lease。
func (c *goplsRootCohortClient) Close() error {
	if c == nil {
		return nil
	}
	return runtimeServerCloseGoplsRootCohortClient(c.Client, c.lease)
}

// RequiresIdleRelease 标记共享 gopls forwarder 必须走 root cohort owner idle 路径。
func (c *goplsRootCohortClient) RequiresIdleRelease() bool {
	return c != nil && c.lease != nil
}

// ReleaseForIdle 是 recycler 面向共享 gopls forwarder 的唯一关闭路径，保留 durable fence 顺序。
func (c *goplsRootCohortClient) ReleaseForIdle() error {
	return c.Close()
}

// runtimeServerResolveResourceLimits 解析主次 RSS/heap 上限并拒绝无法证明安全的组合。
func runtimeServerResolveResourceLimits(env []string) (runtimeServerResourceLimits, error) {
	if err := runtimeServerRejectDeprecatedCohortLimit(env); err != nil {
		return runtimeServerResourceLimits{}, err
	}
	limits := runtimeServerResourceLimits{
		primaryRSSMB:        runtimeDefaultPrimaryRSSLimitMB,
		secondaryRSSMB:      runtimeDefaultSecondaryRSSLimitMB,
		primaryNodeHeapMB:   runtimeDefaultNodePrimaryHeapMB,
		secondaryNodeHeapMB: runtimeDefaultNodeSecondaryHeapMB,
		cohortRSSMB:         runtimeDefaultCohortRSSLimitMB,
	}
	if err := runtimeServerApplyResourceLimitOverrides(env, &limits); err != nil {
		return runtimeServerResourceLimits{}, err
	}
	if err := limits.validate(); err != nil {
		return runtimeServerResourceLimits{}, err
	}
	return limits, nil
}

// runtimeServerRejectDeprecatedCohortLimit 拒绝旧 owner，避免迁移期间形成双写预算。
func runtimeServerRejectDeprecatedCohortLimit(env []string) error {
	if _, configured := runtimeServerEnvLookup(env, multilsp.DeprecatedResourceCohortHardLimitMBEnv); !configured {
		return nil
	}
	return fmt.Errorf(
		"%s is no longer supported; use %s",
		multilsp.DeprecatedResourceCohortHardLimitMBEnv,
		multilsp.ResourceCohortHardLimitMBEnv,
	)
}

// runtimeServerApplyResourceLimitOverrides 把调用期环境覆盖写入单个待校验快照。
func runtimeServerApplyResourceLimitOverrides(env []string, limits *runtimeServerResourceLimits) error {
	settings := []struct {
		key    string
		target *int
	}{
		{runtimePrimaryRSSLimitEnv, &limits.primaryRSSMB},
		{runtimeSecondaryRSSLimitEnv, &limits.secondaryRSSMB},
		{runtimeNodePrimaryHeapEnv, &limits.primaryNodeHeapMB},
		{runtimeNodeSecondaryHeapEnv, &limits.secondaryNodeHeapMB},
		{multilsp.ResourceCohortHardLimitMBEnv, &limits.cohortRSSMB},
	}
	for _, setting := range settings {
		value, configured, err := runtimeServerPositiveMiB(env, setting.key)
		if err != nil {
			return err
		}
		if configured {
			*setting.target = value
		}
	}
	return nil
}

// validate 校验同一个冻结快照内主次进程、Node heap 与 cohort 总预算的关系。
func (limits runtimeServerResourceLimits) validate() error {
	if limits.secondaryRSSMB > limits.primaryRSSMB {
		return fmt.Errorf("%s must not exceed %s", runtimeSecondaryRSSLimitEnv, runtimePrimaryRSSLimitEnv)
	}
	if limits.primaryRSSMB > limits.cohortRSSMB || limits.secondaryRSSMB > limits.cohortRSSMB {
		return fmt.Errorf(
			"%s and %s must not exceed %s",
			runtimePrimaryRSSLimitEnv,
			runtimeSecondaryRSSLimitEnv,
			multilsp.ResourceCohortHardLimitMBEnv,
		)
	}
	if limits.primaryNodeHeapMB >= limits.primaryRSSMB || limits.secondaryNodeHeapMB >= limits.secondaryRSSMB {
		return errors.New("Node heap limits must be lower than their matching RSS limits")
	}
	return nil
}

// runtimeServerPositiveMiB 按 last-wins 环境语义读取严格正整数 MiB 配置。
func runtimeServerPositiveMiB(env []string, key string) (int, bool, error) {
	raw := runtimeServerEnvValue(env, key)
	if raw == "" {
		return 0, false, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, true, fmt.Errorf("%s must be a positive integer MiB value: %q", key, raw)
	}
	return value, true, nil
}

// runtimeServerInitOptions 让会自行派生 tsserver 的 Node adapter 与当前主次堆预算保持一致。
func runtimeServerInitOptions(
	adapter multilsp.LanguageAdapter,
	resolvedInitOptions map[string]any,
	packagedLSP bool,
	serverBinary string,
	serverEnv []string,
) (map[string]any, error) {
	options := runtimeResolvedAdapterInitOptionsWithBinary(adapter, resolvedInitOptions, packagedLSP, serverBinary)
	if _, configured := options["maxTsServerMemory"]; !configured {
		return options, nil
	}
	limits, err := runtimeServerResolveResourceLimits(serverEnv)
	if err != nil {
		return nil, err
	}
	heapLimitMB := limits.primaryNodeHeapMB
	switch runtimeServerEnvironmentValue(serverEnv, multilsp.ResourceCohortRoleEnv) {
	case multilsp.ResourceCohortRolePrimary:
	case multilsp.ResourceCohortRoleSecondary:
		heapLimitMB = limits.secondaryNodeHeapMB
	default:
		return nil, errors.New("Node LSP resource cohort role is missing or invalid")
	}
	options["maxTsServerMemory"] = heapLimitMB
	return options, nil
}

func runtimeServerEnvironmentValue(env []string, key string) string {
	value := ""
	for _, entry := range env {
		entryKey, candidate, ok := strings.Cut(entry, "=")
		if ok && entryKey == key {
			value = strings.TrimSpace(candidate)
		}
	}
	return value
}

// runtimeServerNodeOptions 保留调用方 Node 参数，并用独立堆上限约束 Node 系语言服务。
func runtimeServerNodeOptions(overrides []string, heapLimitMB int) string {
	options := runtimeServerEnvValue(overrides, "NODE_OPTIONS")
	limit := "--max-old-space-size=" + strconv.Itoa(heapLimitMB)
	if options == "" {
		return limit
	}
	return options + " " + limit
}

// appendRuntimeServerEnvironment 以 last-wins 语义替换受管缓存变量并保留其他环境项。
func appendRuntimeServerEnvironment(base, overrides []string) []string {
	overrideKeys := make(map[string]struct{}, len(overrides))
	for _, entry := range overrides {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			overrideKeys[key] = struct{}{}
		}
	}
	result := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			if _, overridden := overrideKeys[key]; overridden {
				continue
			}
		}
		result = append(result, entry)
	}
	return append(result, overrides...)
}

func runtimeSQLiteDiagnosticsWorkspace(adapter multilsp.LanguageAdapter, dir string) (bool, error) {
	if !adapterSupportsLanguage(adapter, "sql") {
		return false, nil
	}
	return isSQLiteDiagnosticsWorkspace(dir)
}
