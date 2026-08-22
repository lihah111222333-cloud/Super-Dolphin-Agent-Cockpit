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

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	// PostgreSQLAuthorityConfigSchema 是 PG authority env binding 的唯一配置 schema。
	PostgreSQLAuthorityConfigSchema   = "remote-ci-postgresql-authority-config/v1"
	postgreSQLAuthorityConfigMaxBytes = 16 << 10
	postgreSQLDriverName              = "pgx"
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
