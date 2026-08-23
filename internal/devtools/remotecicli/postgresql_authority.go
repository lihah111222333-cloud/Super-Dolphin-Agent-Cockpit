package remotecicli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociowner"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociworkflow"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteportable"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteruntime"
)

const (
	// PostgreSQLAuthorityConfigSchema 是 PG authority env binding 的唯一配置 schema。
	PostgreSQLAuthorityConfigSchema   = "remote-ci-postgresql-authority-config/v1"
	postgreSQLAuthorityConfigMaxBytes = 16 << 10
	postgreSQLDriverName              = "pgx"
	executeFailureCodeWorkload        = "workload_failed"
	executeFailureCodeTimeout         = "execution_timeout"
	executeFailureCodeReceipt         = "receipt_missing"
	executeFailureCodeGeneric         = "execution_failed"
)

// PostgreSQLAuthorityRole 是稳定错误允许公开的 authority 角色。
type PostgreSQLAuthorityRole string

const (
	PostgreSQLAuthorityRoleConfig  PostgreSQLAuthorityRole = "config"
	PostgreSQLAuthorityRoleOwner   PostgreSQLAuthorityRole = "owner"
	PostgreSQLAuthorityRoleReceipt PostgreSQLAuthorityRole = "receipt"
	PostgreSQLAuthorityRoleBoth    PostgreSQLAuthorityRole = "owner_receipt"
)

// PostgreSQLAuthorityConfig 只携带两个不同的 DSN 环境变量名，绝不携带 DSN。
type PostgreSQLAuthorityConfig struct {
	SchemaVersion string `json:"schema_version"`
	OwnerDSNEnv   string `json:"owner_dsn_env"`
	ReceiptDSNEnv string `json:"receipt_dsn_env"`
}

// Validate 严格校验 schema 和两个互异的 canonical 环境变量名。
func (config PostgreSQLAuthorityConfig) Validate() error {
	if config.SchemaVersion != PostgreSQLAuthorityConfigSchema {
		return newPostgreSQLAuthorityError(PostgreSQLAuthorityRoleConfig, "invalid_schema")
	}
	if !validPostgreSQLAuthorityEnvName(config.OwnerDSNEnv) || !validPostgreSQLAuthorityEnvName(config.ReceiptDSNEnv) {
		return newPostgreSQLAuthorityError(PostgreSQLAuthorityRoleConfig, "invalid_env_name")
	}
	if config.OwnerDSNEnv == config.ReceiptDSNEnv {
		return newPostgreSQLAuthorityError(PostgreSQLAuthorityRoleConfig, "shared_env_name")
	}
	return ValidatePostgreSQLAuthorityConfigConsumerFields(postgreSQLAuthorityConfigConsumers())
}

// DecodePostgreSQLAuthorityConfig 严格解码 canonical JSON，拒绝未知、重复、null 和尾随内容。
func DecodePostgreSQLAuthorityConfig(data []byte) (PostgreSQLAuthorityConfig, error) {
	if len(data) == 0 || len(data) > postgreSQLAuthorityConfigMaxBytes {
		return PostgreSQLAuthorityConfig{}, newPostgreSQLAuthorityError(PostgreSQLAuthorityRoleConfig, "invalid_size")
	}
	var config PostgreSQLAuthorityConfig
	if err := rejectStrictJSONDuplicates(data); err != nil {
		return PostgreSQLAuthorityConfig{}, newPostgreSQLAuthorityError(PostgreSQLAuthorityRoleConfig, "invalid_json")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return PostgreSQLAuthorityConfig{}, newPostgreSQLAuthorityError(PostgreSQLAuthorityRoleConfig, "invalid_json")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return PostgreSQLAuthorityConfig{}, newPostgreSQLAuthorityError(PostgreSQLAuthorityRoleConfig, "invalid_json")
	}
	if err := config.Validate(); err != nil {
		return PostgreSQLAuthorityConfig{}, err
	}
	canonical, err := json.Marshal(config)
	if err != nil || !bytes.Equal(canonical, data) {
		return PostgreSQLAuthorityConfig{}, newPostgreSQLAuthorityError(PostgreSQLAuthorityRoleConfig, "invalid_json")
	}
	return config, nil
}

// PostgreSQLDatabaseOpener 注入 database/sql 打开动作，测试不得替换生产 driver identity。
type PostgreSQLDatabaseOpener func(driverName, dataSourceName string) (*sql.DB, error)

// PostgreSQLAuthorityDependencies 显式注入环境读取与 database/sql opener。
type PostgreSQLAuthorityDependencies struct {
	LookupEnv func(string) (string, bool)
	Open      PostgreSQLDatabaseOpener
}

// DefaultPostgreSQLAuthorityDependencies 返回 os.LookupEnv 与真实 sql.Open("pgx") 边界。
func DefaultPostgreSQLAuthorityDependencies() PostgreSQLAuthorityDependencies {
	return PostgreSQLAuthorityDependencies{
		LookupEnv: os.LookupEnv,
		Open: func(driverName, dataSourceName string) (*sql.DB, error) {
			return sql.Open(driverName, dataSourceName)
		},
	}
}

// PostgreSQLAuthorities 持有已分别 Ping 成功的 owner 与 receipt database/sql handles。
type PostgreSQLAuthorities struct {
	owner   *sql.DB
	receipt *sql.DB
}

// Owner 返回 owner authority database handle。
func (authorities *PostgreSQLAuthorities) Owner() *sql.DB { return authorities.owner }

// Receipt 返回 receipt authority database handle。
func (authorities *PostgreSQLAuthorities) Receipt() *sql.DB { return authorities.receipt }

// Close 显式关闭 receipt 与 owner；任一失败只返回稳定 role/code。
func (authorities *PostgreSQLAuthorities) Close() error {
	if authorities == nil {
		return newPostgreSQLAuthorityError(PostgreSQLAuthorityRoleBoth, "close_failed")
	}
	return closePostgreSQLAuthorityPair(authorities.owner, authorities.receipt)
}

// OpenPostgreSQLAuthorities 从两个非空秘密环境值分别 sql.Open("pgx") 并 PingContext。
func OpenPostgreSQLAuthorities(ctx context.Context, config PostgreSQLAuthorityConfig, dependencies PostgreSQLAuthorityDependencies) (*PostgreSQLAuthorities, error) {
	if err := validatePostgreSQLAuthorityOpenInput(ctx, config, dependencies); err != nil {
		return nil, err
	}
	ownerDSN, err := readPostgreSQLAuthorityDSN(dependencies.LookupEnv, config.OwnerDSNEnv, PostgreSQLAuthorityRoleOwner)
	if err != nil {
		return nil, err
	}
	receiptDSN, err := readPostgreSQLAuthorityDSN(dependencies.LookupEnv, config.ReceiptDSNEnv, PostgreSQLAuthorityRoleReceipt)
	if err != nil {
		return nil, err
	}
	owner, err := openAndPingPostgreSQLAuthority(ctx, dependencies.Open, ownerDSN, PostgreSQLAuthorityRoleOwner)
	if err != nil {
		return nil, err
	}
	receipt, err := openAndPingPostgreSQLAuthority(ctx, dependencies.Open, receiptDSN, PostgreSQLAuthorityRoleReceipt)
	if err != nil {
		return nil, errors.Join(err, closePostgreSQLAuthority(owner, PostgreSQLAuthorityRoleOwner))
	}
	return &PostgreSQLAuthorities{owner: owner, receipt: receipt}, nil
}

// validatePostgreSQLAuthorityOpenInput 在读取环境前校验 config、context 与依赖。
func validatePostgreSQLAuthorityOpenInput(ctx context.Context, config PostgreSQLAuthorityConfig, dependencies PostgreSQLAuthorityDependencies) error {
	if err := config.Validate(); err != nil {
		return err
	}
	if ctx == nil || ctx.Err() != nil {
		return newPostgreSQLAuthorityError(PostgreSQLAuthorityRoleBoth, "context_invalid")
	}
	if dependencies.LookupEnv == nil || dependencies.Open == nil {
		return newPostgreSQLAuthorityError(PostgreSQLAuthorityRoleBoth, "dependency_missing")
	}
	return nil
}

// readPostgreSQLAuthorityDSN 读取非空秘密值，不 trim、不回显、不缓存。
func readPostgreSQLAuthorityDSN(lookup func(string) (string, bool), name string, role PostgreSQLAuthorityRole) (string, error) {
	value, exists := lookup(name)
	if !exists || strings.TrimSpace(value) == "" {
		return "", newPostgreSQLAuthorityError(role, "dsn_env_missing")
	}
	return value, nil
}

// openAndPingPostgreSQLAuthority 使用固定 pgx driver 打开并验证一个 role。
func openAndPingPostgreSQLAuthority(ctx context.Context, opener PostgreSQLDatabaseOpener, dsn string, role PostgreSQLAuthorityRole) (*sql.DB, error) {
	database, err := opener(postgreSQLDriverName, dsn)
	if database == nil {
		return nil, newPostgreSQLAuthorityError(role, "open_failed")
	}
	if err != nil {
		openErr := newPostgreSQLAuthorityError(role, "open_failed")
		return nil, errors.Join(openErr, closePostgreSQLAuthority(database, role))
	}
	if err := database.PingContext(ctx); err != nil {
		pingErr := newPostgreSQLAuthorityError(role, "ping_failed")
		return nil, errors.Join(pingErr, closePostgreSQLAuthority(database, role))
	}
	return database, nil
}

// closePostgreSQLAuthorityPair 按 receipt→owner 顺序关闭独立 authority handles。
func closePostgreSQLAuthorityPair(owner, receipt *sql.DB) error {
	receiptErr := closePostgreSQLAuthority(receipt, PostgreSQLAuthorityRoleReceipt)
	ownerErr := closePostgreSQLAuthority(owner, PostgreSQLAuthorityRoleOwner)
	return errors.Join(receiptErr, ownerErr)
}

// closePostgreSQLAuthority 关闭一个 handle 并净化底层错误。
func closePostgreSQLAuthority(database *sql.DB, role PostgreSQLAuthorityRole) error {
	if database == nil || database.Close() != nil {
		return newPostgreSQLAuthorityError(role, "close_failed")
	}
	return nil
}

// PostgreSQLAuthorityError 只公开稳定 code/role，不持有或展开 raw driver error。
type PostgreSQLAuthorityError struct {
	code string
	role PostgreSQLAuthorityRole
}

// Error 返回仅含稳定 code/role 的安全文本。
func (failure PostgreSQLAuthorityError) Error() string {
	return "postgresql_authority code=" + failure.code + " role=" + string(failure.role)
}

// Code 返回稳定错误码。
func (failure PostgreSQLAuthorityError) Code() string { return failure.code }

// Role 返回稳定 authority 角色。
func (failure PostgreSQLAuthorityError) Role() PostgreSQLAuthorityRole { return failure.role }

// newPostgreSQLAuthorityError 构造不含 DSN、endpoint、credential 或 cause 的 typed error。
func newPostgreSQLAuthorityError(role PostgreSQLAuthorityRole, code string) error {
	return PostgreSQLAuthorityError{code: code, role: role}
}

// PostgreSQLAuthorityConfigConsumerFields 返回 config consumer registry 的副本。
func PostgreSQLAuthorityConfigConsumerFields() []string {
	return append([]string(nil), postgreSQLAuthorityConfigConsumers()...)
}

// postgreSQLAuthorityConfigConsumers 登记 schema 与两个 env name 的消费边界。
func postgreSQLAuthorityConfigConsumers() []string {
	return []string{"owner_dsn_env", "receipt_dsn_env", "schema_version"}
}

// ValidatePostgreSQLAuthorityConfigConsumerFields 动态拒绝 config consumer missing/stale。
func ValidatePostgreSQLAuthorityConfigConsumerFields(consumers []string) error {
	producer := postgreSQLAuthorityConfigJSONFields()
	want := append([]string(nil), consumers...)
	sort.Strings(want)
	if len(producer) != len(want) {
		return newPostgreSQLAuthorityError(PostgreSQLAuthorityRoleConfig, "field_registry_drift")
	}
	for index := range producer {
		if producer[index] != want[index] || index > 0 && want[index] == want[index-1] {
			return newPostgreSQLAuthorityError(PostgreSQLAuthorityRoleConfig, "field_registry_drift")
		}
	}
	return nil
}

// postgreSQLAuthorityConfigJSONFields 动态枚举 config JSON producer 字段。
func postgreSQLAuthorityConfigJSONFields() []string {
	typeOf := reflect.TypeFor[PostgreSQLAuthorityConfig]()
	fields := make([]string, 0, typeOf.NumField())
	for field := range typeOf.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			return nil
		}
		fields = append(fields, name)
	}
	sort.Strings(fields)
	return fields
}

// validPostgreSQLAuthorityEnvName 校验 1..128 字节的大写 canonical env 名。
func validPostgreSQLAuthorityEnvName(value string) bool {
	if len(value) == 0 || len(value) > 128 || value[0] < 'A' || value[0] > 'Z' {
		return false
	}
	for _, char := range value[1:] {
		if !validPostgreSQLAuthorityEnvChar(char) {
			return false
		}
	}
	return true
}

// validPostgreSQLAuthorityEnvChar 判断后续字符是否属于大写 env 字符集。
func validPostgreSQLAuthorityEnvChar(char rune) bool {
	return char == '_' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9'
}

// rejectStrictJSONDuplicates 拒绝 null、重复键和异常 JSON 容器。
func rejectStrictJSONDuplicates(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	return walkStrictJSONValue(decoder)
}

func walkStrictJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return errors.New("JSON null is not allowed")
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		return walkStrictJSONObject(decoder)
	case '[':
		return walkStrictJSONArray(decoder)
	default:
		return errors.New("JSON delimiter is invalid")
	}
}

func walkStrictJSONObject(decoder *json.Decoder) error {
	seen := map[string]struct{}{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := key.(string)
		if !ok {
			return errors.New("JSON object key is invalid")
		}
		if _, duplicate := seen[name]; duplicate {
			return errors.New("JSON object key is duplicated")
		}
		seen[name] = struct{}{}
		if err := walkStrictJSONValue(decoder); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return errors.New("JSON object is unterminated")
	}
	return nil
}

func walkStrictJSONArray(decoder *json.Decoder) error {
	for decoder.More() {
		if err := walkStrictJSONValue(decoder); err != nil {
			return err
		}
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim(']') {
		return errors.New("JSON array is unterminated")
	}
	return nil
}

var (
	// ErrExecuteCapacityBlocked 表示 canonical blocked terminal 已持久化并写入 stdout。
	ErrExecuteCapacityBlocked = errors.New("remote CI execution is capacity blocked")
	// ErrExecuteComposition 表示 execute production composition 缺失或状态不完整。
	ErrExecuteComposition = errors.New("remote CI execute composition is invalid")
)

type executeReceiptAuthority interface {
	heterociexec.ReceiptStore
	AppendEvent(context.Context, heterociexec.PostgreSQLReceiptEvent) error
	WriteTerminalReceipt(context.Context, heterociexec.PostgreSQLTerminalReceipt) error
	ReadTerminalReceipt(context.Context, string) (heterociexec.PostgreSQLTerminalReceipt, bool, error)
}

type executeComponents struct {
	owner    heterociworkflow.DurableOwner
	receipts executeReceiptAuthority
	probe    heteroci.ProbeTransport
	binder   heterociworkflow.ExecutorBinder
	close    func() error
}

func (components executeComponents) validate() error {
	if isNilExecuteDependency(components.owner) || isNilExecuteDependency(components.receipts) || isNilExecuteDependency(components.probe) || isNilExecuteDependency(components.binder) || components.close == nil {
		return ErrExecuteComposition
	}
	return nil
}

func isNilExecuteDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

type executeComponentFactory func(context.Context, executePreparedInputs, func() time.Time) (executeComponents, error)
type executeWorkflowRunner func(context.Context, heterociworkflow.Dependencies, heterociworkflow.RunRequest) (heterociworkflow.RunResult, error)

type executeCommandDependencies struct {
	clock      func() time.Time
	components executeComponentFactory
	run        executeWorkflowRunner
}

func defaultExecuteCommandDependencies() executeCommandDependencies {
	return executeCommandDependencies{
		clock:      func() time.Time { return time.Now().UTC() },
		components: openProductionExecuteComponents,
		run:        heterociworkflow.Execute,
	}
}

// dispatchExecute 是公开 execute 的唯一 production composition 入口。
func dispatchExecute(args []string, stdout, stderr io.Writer) error {
	return dispatchExecuteWithDependencies(args, stdout, stderr, defaultExecuteCommandDependencies())
}

func dispatchExecuteWithDependencies(args []string, stdout, stderr io.Writer, dependencies executeCommandDependencies) error {
	if stdout == nil || stderr == nil || dependencies.clock == nil || dependencies.components == nil || dependencies.run == nil {
		return ErrExecuteComposition
	}
	_, _, runtimeConfigPath, err := parseExecuteArgs(args)
	if err != nil {
		return err
	}
	runtimeConfig, err := loadExecuteRuntimeConfig(runtimeConfigPath)
	if err != nil {
		return err
	}
	requestTimeout := time.Duration(runtimeConfig.RequestTimeoutMS) * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	prepared, err := prepareExecuteInputsWithConfig(ctx, args, runtimeConfig)
	if err != nil {
		return err
	}
	return runPreparedExecute(ctx, prepared, stdout, stderr, dependencies)
}

func runPreparedExecute(ctx context.Context, prepared executePreparedInputs, stdout, stderr io.Writer, dependencies executeCommandDependencies) error {
	components, err := dependencies.components(ctx, prepared, dependencies.clock)
	if err != nil {
		return newCLIFailure("open execute components", err)
	}
	if err := components.validate(); err != nil {
		return errors.Join(err, closeExecuteComponents(components))
	}
	coordinator := newExecuteLifecycleCoordinator(components.receipts, stderr, dependencies.clock, prepared.Portable.Manifest.LogPolicy)
	result, runErr := dependencies.run(ctx, executeWorkflowDependencies(prepared, components, coordinator, dependencies.clock), prepared.RunRequest)
	outcome, outcomeErr := coordinator.finish(ctx, result, runErr)
	closeErr := components.close()
	if err := publishExecuteTerminal(stdout, outcome.bytes, prepared.Portable.Manifest.LogPolicy.MaxResultBytes, outcomeErr, closeErr); err != nil {
		return err
	}
	if outcome.blocked {
		return ErrExecuteCapacityBlocked
	}
	return nil
}

func publishExecuteTerminal(stdout io.Writer, encoded []byte, limit int64, outcomeErr, closeErr error) error {
	if outcomeErr != nil || closeErr != nil {
		var outputErr error
		if len(encoded) != 0 {
			outputErr = writeExecuteTerminal(stdout, encoded, limit)
		}
		return errors.Join(outcomeErr, closeErr, outputErr)
	}
	return writeExecuteTerminal(stdout, encoded, limit)
}

func closeExecuteComponents(components executeComponents) error {
	if components.close == nil {
		return nil
	}
	return components.close()
}

func executeWorkflowDependencies(prepared executePreparedInputs, components executeComponents, coordinator *executeLifecycleCoordinator, clock func() time.Time) heterociworkflow.Dependencies {
	return heterociworkflow.Dependencies{
		Owner: components.owner, ProbeTransport: components.probe, Executors: components.binder,
		LoadManifest: func(path string) (heteroci.Manifest, error) {
			if path != prepared.Runtime.TopologyManifestPath {
				return heteroci.Manifest{}, ErrExecuteComposition
			}
			return prepared.Topology, nil
		},
		AcquireSnapshot: heteroci.AcquireSnapshot, Now: clock,
		CleanupTimeout:    time.Duration(prepared.Runtime.CleanupTimeoutMS) * time.Millisecond,
		LifecycleObserver: coordinator, CapacityBlockedObserver: coordinator,
	}
}

func openProductionExecuteComponents(ctx context.Context, prepared executePreparedInputs, clock func() time.Time) (executeComponents, error) {
	authorityConfig := PostgreSQLAuthorityConfig{SchemaVersion: PostgreSQLAuthorityConfigSchema, OwnerDSNEnv: prepared.Runtime.OwnerDSNEnv, ReceiptDSNEnv: prepared.Runtime.ReceiptDSNEnv}
	authorities, err := OpenPostgreSQLAuthorities(ctx, authorityConfig, DefaultPostgreSQLAuthorityDependencies())
	if err != nil {
		return executeComponents{}, err
	}
	components, err := bindProductionExecuteComponents(ctx, prepared, clock, authorities)
	if err != nil {
		return executeComponents{}, errors.Join(err, authorities.Close())
	}
	components.close = authorities.Close
	return components, nil
}

func bindProductionExecuteComponents(ctx context.Context, prepared executePreparedInputs, clock func() time.Time, authorities *PostgreSQLAuthorities) (executeComponents, error) {
	owner, err := heterociowner.NewPostgreSQLOwnerWithClock(ctx, authorities.Owner(), prepared.OwnerScope, clock)
	if err != nil {
		return executeComponents{}, err
	}
	durableOwner, err := heterociowner.NewPostgreSQLDurableOwner(owner)
	if err != nil {
		return executeComponents{}, err
	}
	receipts, err := heterociexec.NewPostgreSQLReceiptStore(ctx, authorities.Receipt())
	if err != nil {
		return executeComponents{}, err
	}
	runtimeTransport, err := remoteruntime.NewExecTransport(prepared.Runtime)
	if err != nil {
		return executeComponents{}, err
	}
	probe, err := NewRuntimeProbeTransport(runtimeTransport, prepared.RuntimeBinding.Target, prepared.Topology)
	if err != nil {
		return executeComponents{}, err
	}
	moduleIdentity, err := deriveDetectedRepositoryModuleIdentity(ctx, prepared.Repository)
	if err != nil {
		return executeComponents{}, err
	}
	binder, err := NewExactExecutorBinder(runtimeTransport, prepared.Portable, prepared.Repository.Workloads, moduleIdentity, func(remoteruntime.NodeBinding) (heterociexec.ReceiptStore, error) { return receipts, nil })
	if err != nil {
		return executeComponents{}, err
	}
	return executeComponents{owner: durableOwner, receipts: receipts, probe: probe, binder: binder}, nil
}

type executeTerminalOutcome struct {
	bytes   []byte
	blocked bool
}

type executeLifecycleCoordinator struct {
	receipts   executeReceiptAuthority
	stderr     io.Writer
	clock      func() time.Time
	logPolicy  remoteportable.PortableManifestLogPolicy
	prepared   *heterociworkflow.PreparedRunSnapshot
	first      []heterociexec.PostgreSQLReceiptEvent
	stageTimes []time.Time
	blocked    *executeTerminalOutcome
}

// executeLifecycleCoordinator 是唯一发布 remote-ci-event/v1 的 outer owner；
// helper stderr 只由 remoteruntime 归档为 remote-ci-helper-log/v1 evidence，不是外部 event channel。

func newExecuteLifecycleCoordinator(receipts executeReceiptAuthority, stderr io.Writer, clock func() time.Time, policy remoteportable.PortableManifestLogPolicy) *executeLifecycleCoordinator {
	return &executeLifecycleCoordinator{receipts: receipts, stderr: stderr, clock: clock, logPolicy: policy}
}

func (coordinator *executeLifecycleCoordinator) validateInitialObservation() error {
	if coordinator == nil || coordinator.prepared != nil || coordinator.blocked != nil || coordinator.receipts == nil || coordinator.stderr == nil || coordinator.clock == nil {
		return ErrExecuteComposition
	}
	return nil
}

// ObservePreparedRun 在首个 Start 前持久化并输出 request_validation 到 reservation。
func (coordinator *executeLifecycleCoordinator) ObservePreparedRun(ctx context.Context, snapshot heterociworkflow.PreparedRunSnapshot) error {
	if err := coordinator.validateInitialObservation(); err != nil {
		return err
	}
	if err := snapshot.Validate(); err != nil {
		return err
	}
	stageTimes, err := preparedExecuteStageTimes(snapshot.SourceSnapshot, coordinator.clock)
	if err != nil {
		return err
	}
	shards, err := preparedExecuteTerminalShards(snapshot, stageTimes[3])
	if err != nil {
		return err
	}
	projection, err := projectPreparedExecuteLifecycle(snapshot, shards, stageTimes)
	if err != nil {
		return err
	}
	if err := coordinator.appendAndWriteEvents(ctx, projection.Events[:4]); err != nil {
		return err
	}
	copy := snapshot
	coordinator.prepared = &copy
	coordinator.first = append([]heterociexec.PostgreSQLReceiptEvent(nil), projection.Events[:4]...)
	coordinator.stageTimes = append([]time.Time(nil), stageTimes...)
	return nil
}

// ObserveCapacityBlocked 发布完整八阶段空分片 blocked lifecycle 和 terminal receipt。
func (coordinator *executeLifecycleCoordinator) ObserveCapacityBlocked(ctx context.Context, snapshot heterociworkflow.CapacityBlockedRunSnapshot) error {
	if err := coordinator.validateInitialObservation(); err != nil {
		return err
	}
	if err := snapshot.Validate(); err != nil {
		return err
	}
	stageTimes, err := blockedExecuteStageTimes(snapshot.SourceSnapshot, coordinator.clock)
	if err != nil {
		return err
	}
	emptyShards := []heterociexec.PostgreSQLTerminalShard{}
	nodeSetDigest, err := heterociexec.PostgreSQLTerminalNodeSetDigest(emptyShards)
	if err != nil {
		return err
	}
	input := heterociexec.PostgreSQLLifecycleProjectionInput{
		OperationID: string(snapshot.ExternalOperationID), RequestDigest: snapshot.RequestDigest,
		ManifestDigest: snapshot.ManifestDigest, PlanDigest: snapshot.PlanDigest,
		ExecutorBindingDigest: blockedExecuteBindingDigest(snapshot.RequestDigest, snapshot.PlanDigest), NodeSetDigest: nodeSetDigest,
		AuthorityGeneration: int64(snapshot.Fence.OwnerEpoch), StageTimes: stageTimes, TerminalShards: emptyShards,
		Status: heterociexec.PostgreSQLReceiptStatusBlocked, CleanupState: heterociexec.PostgreSQLReceiptCleanupVerified,
		ExitCode: -1, CapacityCode: "capacity_blocked",
	}
	projection, err := heterociexec.ProjectPostgreSQLLifecycle(input)
	if err != nil {
		return err
	}
	outcome, err := coordinator.persistTerminalProjection(ctx, projection, 0)
	if err != nil {
		return err
	}
	outcome.blocked = true
	coordinator.blocked = &outcome
	return nil
}

func (coordinator *executeLifecycleCoordinator) finish(ctx context.Context, result heterociworkflow.RunResult, runErr error) (executeTerminalOutcome, error) {
	if runErr != nil {
		if coordinator == nil {
			return executeTerminalOutcome{}, newCLIFailure("execute workflow failed", runErr)
		}
		var blocked *heterociworkflow.CapacityBlockedRunError
		if errors.As(runErr, &blocked) && blocked != nil && coordinator.blocked != nil {
			return *coordinator.blocked, nil
		}
		if coordinator.prepared != nil && coordinator.blocked == nil {
			outcome, err := coordinator.finishFailed(ctx, runErr)
			if err != nil {
				return executeTerminalOutcome{}, errors.Join(newCLIFailure("execute workflow failed", runErr), newCLIFailure("persist failed terminal", err))
			}
			return outcome, newCLIFailure("execute workflow failed", runErr)
		}
		return executeTerminalOutcome{}, newCLIFailure("execute workflow failed", runErr)
	}
	if coordinator == nil || coordinator.prepared == nil || coordinator.blocked != nil {
		return executeTerminalOutcome{}, ErrExecuteComposition
	}
	return coordinator.finishSucceeded(ctx, result)
}

func (coordinator *executeLifecycleCoordinator) finishFailed(ctx context.Context, runErr error) (executeTerminalOutcome, error) {
	if coordinator == nil || coordinator.prepared == nil || coordinator.blocked != nil || runErr == nil {
		return executeTerminalOutcome{}, ErrExecuteComposition
	}
	if errors.Is(runErr, heterociworkflow.ErrCleanupIncomplete) {
		return executeTerminalOutcome{}, ErrExecuteComposition
	}
	finishedAt := coordinator.clock()
	stageTimes := append([]time.Time(nil), coordinator.stageTimes...)
	for index := 4; index < len(stageTimes); index++ {
		stageTimes[index] = maxExecuteTime(stageTimes[index-1], finishedAt)
	}
	if err := validateExecuteStageTimes(stageTimes); err != nil {
		return executeTerminalOutcome{}, err
	}
	classification := classifyExecuteFailure(runErr)
	code := classification.code
	shards, err := failedExecuteTerminalShards(*coordinator.prepared, stageTimes[4], stageTimes[5], code)
	if err != nil {
		return executeTerminalOutcome{}, err
	}
	nodeSetDigest, err := heterociexec.PostgreSQLTerminalNodeSetDigest(shards)
	if err != nil {
		return executeTerminalOutcome{}, err
	}
	input := heterociexec.PostgreSQLLifecycleProjectionInput{
		OperationID: string(coordinator.prepared.ExternalOperationID), RequestDigest: coordinator.prepared.RequestDigest,
		ManifestDigest: coordinator.prepared.ManifestDigest, PlanDigest: coordinator.prepared.BindingPlan.PlanDigest,
		ExecutorBindingDigest: coordinator.prepared.BindingDigest, NodeSetDigest: nodeSetDigest,
		AuthorityGeneration: int64(coordinator.prepared.Fence.OwnerEpoch), StageTimes: stageTimes, TerminalShards: shards,
		Status: heterociexec.PostgreSQLReceiptStatusFailed, CleanupState: heterociexec.PostgreSQLReceiptCleanupVerified, ExitCode: 1, ErrorCode: code,
		FailureStage: classification.stage,
	}
	projection, err := heterociexec.ProjectPostgreSQLLifecycle(input)
	if err != nil {
		return executeTerminalOutcome{}, err
	}
	if err := compareExecuteFirstEvents(coordinator.first, projection.Events[:4]); err != nil {
		return executeTerminalOutcome{}, err
	}
	return coordinator.persistTerminalProjection(ctx, projection, 4)
}

func executeFailureCode(err error) string {
	return classifyExecuteFailure(err).code
}

type executeFailureClassification struct {
	code  string
	stage heterociexec.PostgreSQLReceiptEventStage
}

func classifyExecuteFailure(err error) executeFailureClassification {
	classification := executeFailureClassification{code: executeFailureCodeGeneric, stage: heterociexec.PostgreSQLReceiptEventStageExecute}
	if errors.Is(err, heterociworkflow.ErrExecutorBinding) {
		classification.stage = heterociexec.PostgreSQLReceiptEventStageMaterialize
		return classification
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, remoteruntime.ErrCommandTimedOut):
		classification.code = executeFailureCodeTimeout
	case errors.Is(err, remoteruntime.ErrCommandFailed):
		classification.code = executeFailureCodeGeneric
	case errors.Is(err, heterociworkflow.ErrBatchReceiptRequired):
		classification.code = executeFailureCodeReceipt
	case errors.Is(err, heterociworkflow.ErrBatchFailed):
		classification.code = executeFailureCodeWorkload
	}
	return classification
}

func (coordinator *executeLifecycleCoordinator) finishSucceeded(ctx context.Context, result heterociworkflow.RunResult) (executeTerminalOutcome, error) {
	shards, startedAt, finishedAt, err := succeededExecuteTerminalShards(*coordinator.prepared, result)
	if err != nil {
		return executeTerminalOutcome{}, err
	}
	stageTimes := append([]time.Time(nil), coordinator.stageTimes...)
	stageTimes[4], stageTimes[5] = startedAt, finishedAt
	stageTimes[6], stageTimes[7] = coordinator.clock(), coordinator.clock()
	if err := validateExecuteStageTimes(stageTimes); err != nil {
		return executeTerminalOutcome{}, err
	}
	nodeSetDigest, err := heterociexec.PostgreSQLTerminalNodeSetDigest(shards)
	if err != nil {
		return executeTerminalOutcome{}, err
	}
	input := heterociexec.PostgreSQLLifecycleProjectionInput{
		OperationID: string(coordinator.prepared.ExternalOperationID), RequestDigest: coordinator.prepared.RequestDigest,
		ManifestDigest: coordinator.prepared.ManifestDigest, PlanDigest: result.BindingPlan.PlanDigest,
		ExecutorBindingDigest: result.ExecutorBindingDigest, NodeSetDigest: nodeSetDigest,
		AuthorityGeneration: int64(result.Fence.OwnerEpoch), StageTimes: stageTimes, TerminalShards: shards,
		Status: heterociexec.PostgreSQLReceiptStatusSucceeded, CleanupState: heterociexec.PostgreSQLReceiptCleanupVerified, ExitCode: 0,
	}
	projection, err := heterociexec.ProjectPostgreSQLLifecycle(input)
	if err != nil {
		return executeTerminalOutcome{}, err
	}
	if err := compareExecuteFirstEvents(coordinator.first, projection.Events[:4]); err != nil {
		return executeTerminalOutcome{}, err
	}
	return coordinator.persistTerminalProjection(ctx, projection, 4)
}

func (coordinator *executeLifecycleCoordinator) appendAndWriteEvents(ctx context.Context, events []heterociexec.PostgreSQLReceiptEvent) error {
	for _, event := range events {
		if err := coordinator.receipts.AppendEvent(ctx, event); err != nil {
			return err
		}
		if err := writeExecuteEvent(coordinator.stderr, event, coordinator.logPolicy.MaxEventBytes); err != nil {
			return err
		}
	}
	return nil
}

func (coordinator *executeLifecycleCoordinator) persistTerminalProjection(ctx context.Context, projection heterociexec.PostgreSQLLifecycleProjection, eventOffset int) (executeTerminalOutcome, error) {
	if err := coordinator.appendAndWriteEvents(ctx, projection.Events[eventOffset:]); err != nil {
		return executeTerminalOutcome{}, err
	}
	if err := coordinator.receipts.WriteTerminalReceipt(ctx, projection.Receipt); err != nil {
		return executeTerminalOutcome{}, err
	}
	persisted, found, err := coordinator.receipts.ReadTerminalReceipt(ctx, projection.Receipt.OperationID)
	if err != nil || !found {
		return executeTerminalOutcome{}, errors.Join(ErrExecuteComposition, err)
	}
	wantBytes, _, err := heterociexec.EncodePostgreSQLTerminalReceipt(projection.Receipt)
	if err != nil {
		return executeTerminalOutcome{}, err
	}
	gotBytes, _, err := heterociexec.EncodePostgreSQLTerminalReceipt(persisted)
	if err != nil || !bytes.Equal(wantBytes, gotBytes) {
		return executeTerminalOutcome{}, errors.Join(ErrExecuteComposition, err)
	}
	return executeTerminalOutcome{bytes: wantBytes}, nil
}
