package main

import (
	"errors"
	"fmt"
	"runtime"
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
	goplsRootController multilsp.GoplsRootCohortController,
	handler protocol.NotificationHandler,
) (multilsp.Client, error) {
	dir := strings.TrimSpace(rootDir)
	if dir == "" {
		dir = root
	}
	serverBinary := binary.Get()
	var goplsLease *multilsp.GoplsRootCohortLease
	if runtime.GOOS != "windows" && runtimeServerUsesSharedGoplsDaemon(command) {
		config, configErr := runtimeServerGoplsRootCohortConfig(command, serverBinary, dir, env)
		if configErr != nil {
			return nil, configErr
		}
		if goplsRootController == nil {
			return nil, fmt.Errorf("%w for cohort %s", multilsp.ErrGoplsRootCohortDurabilityUnsupported, config.CohortID)
		}
		lease, leaseErr := goplsRootController.AcquireLease(config)
		if leaseErr != nil {
			return nil, leaseErr
		}
		goplsLease = &lease
	}
	serverArgs, err := runtimeServerArgsForOS(command, serverBinary, env, runtime.GOOS, dir)
	if err != nil {
		if goplsLease != nil {
			return nil, errors.Join(err, goplsLease.Release())
		}
		return nil, err
	}
	resourceCommand := command
	resourceCommand.Args = serverArgs
	serverEnv, err := runtimeServerEnvironment(
		resourceCommand,
		serverBinary,
		dir,
		adapter.LanguageIDs(),
		env,
		runtimeAdapterUsesNode(adapter),
	)
	if err != nil {
		if goplsLease != nil {
			return nil, errors.Join(err, goplsLease.Release())
		}
		return nil, err
	}
	sqliteWorkspace, err := runtimeSQLiteDiagnosticsWorkspace(adapter, dir)
	if err != nil {
		cleanupErr := errors.Join(multilsp.ReleaseResourceCohortLease(serverEnv), releaseGoplsRootLease(goplsLease))
		return nil, errors.Join(err, cleanupErr)
	}
	notificationHandler := handler
	if sqliteWorkspace {
		notificationHandler = &sqlDiagnosticNotificationHandler{root: dir, next: handler}
	}
	initOptions, err := runtimeServerInitOptions(adapter, packagedLSP, serverBinary, serverEnv)
	if err != nil {
		cleanupErr := errors.Join(multilsp.ReleaseResourceCohortLease(serverEnv), releaseGoplsRootLease(goplsLease))
		return nil, errors.Join(err, cleanupErr)
	}
	client, err := multilsp.NewClientWithOptions(multilsp.Options{
		Binary:              serverBinary,
		Args:                serverArgs,
		Dir:                 dir,
		Env:                 serverEnv,
		InitOptions:         initOptions,
		NotificationHandler: notificationHandler,
	})
	if err != nil {
		cleanupErr := errors.Join(multilsp.ReleaseResourceCohortLease(serverEnv), releaseGoplsRootLease(goplsLease))
		return nil, errors.Join(err, cleanupErr)
	}
	if !sqliteWorkspace {
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

// Close 把真实 forwarder transport 交给 root cohort owner；最后 member 的
// transport 由 durable idle-drain 在 deadline/fence 复核后关闭，避免先关闭
// 自身再把已关闭 callback 交给 15 分钟 drain。
func (c *goplsRootCohortClient) Close() error {
	if c == nil {
		return nil
	}
	if c.lease == nil {
		return c.Client.Close()
	}
	return c.lease.ReleaseWithOwner(func() error {
		return c.Client.Close()
	})
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
	if limits.secondaryRSSMB >= limits.primaryRSSMB {
		return fmt.Errorf("%s must be lower than %s", runtimeSecondaryRSSLimitEnv, runtimePrimaryRSSLimitEnv)
	}
	if limits.secondaryRSSMB >= 2*1024 {
		return fmt.Errorf("%s must stay below 2048 MiB", runtimeSecondaryRSSLimitEnv)
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
	packagedLSP bool,
	serverBinary string,
	serverEnv []string,
) (map[string]any, error) {
	options := runtimeAdapterInitOptionsWithBinary(adapter, packagedLSP, serverBinary)
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
