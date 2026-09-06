// Package messageassociation 定义 message/reply 关联的 transport-neutral 候选合同。
//
// 本包只校验不可授权的候选 DTO；不生成 principal，不注册 transport/provider，
// 不访问 EventStore、数据库或 Fx，也不触发任何外部 effect。候选必须在上游 owner
// 完成授权、持久化和真实执行前再次验证。
package messageassociation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

const (
	// SchemaVersion 是候选合同的固定版本。
	SchemaVersion int64 = 1
	// ObjectKind 是顶层 typed object kind。
	ObjectKind = "message_reply_association"
	// ScopeKind 是关联合同允许的 scope kind。
	ScopeKind = "orchestration_message_reply"
	// CardinalityOneToOne 表示一个 message 至多对应一个 reply，反之亦然。
	CardinalityOneToOne Cardinality = "one_to_one"
	// CommandVersion 是当前 message/reply command 版本。
	CommandVersion         int64 = 1
	maxIntentJSONBytes           = 1 << 20
	maxIntentJSONDepth           = 16
	maxIntentObjectMembers       = 64
)

// Cardinality 是关联两端的固定一对一枚举，不能用任意数量或裸 correlation 字段替代。
type Cardinality string

type associationError string

// Error 返回候选合同的稳定哨兵文本。
func (e associationError) Error() string { return string(e) }

const (
	// ErrInvalidAssociation 表示候选合同缺失字段或违反围栏/摘要绑定。
	ErrInvalidAssociation associationError = "message reply association is invalid"
	// ErrBareAssociation 表示使用 correlation_id/reply_to 等裸字段建立关联。
	ErrBareAssociation associationError = "bare correlation or reply fields cannot establish association"
	// ErrAssociationConflict 表示同一 parent command 下出现不一致候选。
	ErrAssociationConflict associationError = "message reply association conflicts with parent command"
)

// TypedObject 固定候选对象的 kind、版本和对象摘要，阻断未版本化 map。
type TypedObject struct {
	Kind    string `json:"kind"`
	Version int64  `json:"version"`
	Digest  string `json:"digest"`
}

// Scope 固定关联所在的逻辑范围；scope 不是调用者 principal，也不提供授权。
type Scope struct {
	Kind    string `json:"kind"`
	Ref     string `json:"ref"`
	Version int64  `json:"version"`
	Digest  string `json:"digest"`
}

// TurnFence 固定单个 turn 的代际与摘要。
type TurnFence struct {
	TurnID         string `json:"turn_id"`
	TurnGeneration int64  `json:"turn_generation"`
	TurnDigest     string `json:"turn_digest"`
}

// EndpointFence 将 common.ExecutionFence 与 turn fence 绑定为完整端点围栏。
type EndpointFence struct {
	Execution common.ExecutionFence `json:"execution"`
	Turn      TurnFence             `json:"turn"`
}

// ParentCommand 是 message/reply 共同引用的稳定父命令快照。
type ParentCommand struct {
	CommandID      string `json:"command_id"`
	CommandVersion int64  `json:"command_version"`
	IntentDigest   string `json:"intent_digest"`
	FenceDigest    string `json:"fence_digest"`
}

// MessageCommand 是独立、幂等的 message command 声明。
type MessageCommand struct {
	CommandID            string `json:"command_id"`
	CommandVersion       int64  `json:"command_version"`
	ParentCommandID      string `json:"parent_command_id"`
	ParentCommandVersion int64  `json:"parent_command_version"`
	IntentDigest         string `json:"intent_digest"`
	IdempotencyNamespace string `json:"idempotency_namespace"`
	IdempotencyKeyDigest string `json:"idempotency_key_digest"`
	FenceDigest          string `json:"fence_digest"`
}

// ReplyCommand 是独立、幂等的 reply command 声明，不是 message command 的别名。
type ReplyCommand struct {
	CommandID            string `json:"command_id"`
	CommandVersion       int64  `json:"command_version"`
	ParentCommandID      string `json:"parent_command_id"`
	ParentCommandVersion int64  `json:"parent_command_version"`
	IntentDigest         string `json:"intent_digest"`
	IdempotencyNamespace string `json:"idempotency_namespace"`
	IdempotencyKeyDigest string `json:"idempotency_key_digest"`
	FenceDigest          string `json:"fence_digest"`
}

// ContentRef 只携带内容 owner 引用和摘要，不携带正文或 credential。
type ContentRef struct {
	Ref     string `json:"ref"`
	Version int64  `json:"version"`
	Digest  string `json:"digest"`
}

// Association 是不可授权的 message/reply 关联候选。
type Association struct {
	SchemaVersion int64          `json:"schema_version"`
	Object        TypedObject    `json:"object"`
	Scope         Scope          `json:"scope"`
	Cardinality   Cardinality    `json:"cardinality"`
	Parent        ParentCommand  `json:"parent"`
	Message       MessageCommand `json:"message"`
	Reply         ReplyCommand   `json:"reply"`
	Source        EndpointFence  `json:"source"`
	Target        EndpointFence  `json:"target"`
	// SourcePrincipalRef 是 transport/auth owner 产生的 opaque principal 引用；
	// 本包只携带并校验它，不创建、解析或提升为授权。
	SourcePrincipalRef string     `json:"source_principal_ref"`
	ClientIntentDigest string     `json:"client_intent_digest"`
	MessageContent     ContentRef `json:"message_content"`
	ReplyContent       ContentRef `json:"reply_content"`
}

// Validate 验证版本、typed object/scope/cardinality、命令独立性及完整 fence。
func (a Association) Validate() error {
	if err := a.validateEnvelope(); err != nil {
		return err
	}
	if err := a.Message.validate(a.Parent, "message"); err != nil {
		return err
	}
	if err := a.Reply.validate(a.Parent, "reply"); err != nil {
		return err
	}
	if a.Message.CommandID == a.Reply.CommandID {
		return invalid("message and reply command IDs must differ")
	}
	if err := validateDistinctCommands(a.Parent, a.Message.CommandID, a.Reply.CommandID, a.Message.IdempotencyNamespace, a.Reply.IdempotencyNamespace, a.Message.IdempotencyKeyDigest, a.Reply.IdempotencyKeyDigest); err != nil {
		return err
	}
	if err := a.MessageContent.validate("message_content"); err != nil {
		return err
	}
	if err := a.ReplyContent.validate("reply_content"); err != nil {
		return err
	}
	return nil
}

// validateEnvelope 校验候选的版本、typed object/scope、两端围栏和来源 principal 引用。
func (a Association) validateEnvelope() error {
	if a.SchemaVersion != SchemaVersion {
		return invalid("schema_version must equal 1")
	}
	if err := a.Object.validate(); err != nil {
		return err
	}
	if err := a.Scope.validate(); err != nil {
		return err
	}
	if a.Cardinality != CardinalityOneToOne {
		return invalid("cardinality must be one_to_one")
	}
	if err := a.Parent.validate(); err != nil {
		return err
	}
	if err := a.Source.validate("source"); err != nil {
		return err
	}
	if err := a.Target.validate("target"); err != nil {
		return err
	}
	if err := validateText("source_principal_ref", a.SourcePrincipalRef, 512); err != nil {
		return err
	}
	return validateDigest("client_intent_digest", a.ClientIntentDigest)
}

// validateDistinctCommands 确保 parent、message、reply 及两套幂等命名空间彼此独立。
func validateDistinctCommands(parent ParentCommand, messageID, replyID, messageNamespace, replyNamespace, messageKey, replyKey string) error {
	if parent.CommandID == messageID || parent.CommandID == replyID {
		return invalid("parent and child command IDs must differ")
	}
	if messageID == replyID {
		return invalid("message and reply command IDs must differ")
	}
	if messageNamespace == replyNamespace {
		return invalid("message and reply idempotency namespaces must differ")
	}
	if messageKey == replyKey {
		return invalid("message and reply idempotency key digests must differ")
	}
	return nil
}

// ComputeClientIntentDigest 返回 canonical JSON 的 SHA-256 摘要；它不代表授权签名。
func ComputeClientIntentDigest(intent any) (string, error) {
	if intent == nil {
		return "", invalid("client intent is nil")
	}
	if err := validateIntentValue(reflect.ValueOf(intent), 0); err != nil {
		return "", err
	}
	payload, err := json.Marshal(intent)
	if err != nil {
		return "", fmt.Errorf("%w: encode client intent: %v", ErrInvalidAssociation, err)
	}
	if len(payload) == 0 || payload[0] != '{' {
		return "", invalid("client intent must be a JSON object")
	}
	if err := validateJSONShape(payload); err != nil {
		return "", fmt.Errorf("%w: client intent JSON is not canonical", err)
	}
	var normalized any
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&normalized); err != nil {
		return "", invalid("client intent JSON decode failed")
	}
	canonical, err := json.Marshal(normalized)
	if err != nil || !bytes.Equal(payload, canonical) {
		return "", invalid("client intent JSON is not canonical")
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

// validateIntentValue 在 marshal 前递归检查字符串 UTF-8 与结构深度，保留合法 U+FFFD。
func validateIntentValue(value reflect.Value, depth int) error {
	if !value.IsValid() {
		return nil
	}
	if hasCustomJSONMarshaler(value) {
		return invalid("client intent custom JSON marshaler is not allowed")
	}
	if value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		return validateIntentPointer(value, depth)
	}
	return validateIntentKind(value, depth)
}

// validateIntentKind 分派 primitive 与复合 intent 值的递归校验。
func validateIntentKind(value reflect.Value, depth int) error {
	if depth > maxIntentJSONDepth {
		return invalid("client intent nesting exceeds bounded depth")
	}
	switch value.Kind() {
	case reflect.String:
		if !utf8.ValidString(value.String()) {
			return invalid("client intent contains invalid UTF-8")
		}
	case reflect.Map:
		return validateIntentMap(value, depth)
	case reflect.Struct:
		return validateIntentStruct(value, depth)
	case reflect.Array, reflect.Slice:
		return validateIntentSequence(value, depth)
	}
	return nil
}

func hasCustomJSONMarshaler(value reflect.Value) bool {
	marshalerType := reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	if value.Type().Implements(marshalerType) {
		return true
	}
	if value.Kind() != reflect.Pointer && reflect.PointerTo(value.Type()).Implements(marshalerType) {
		return true
	}
	return value.CanAddr() && value.Addr().Type().Implements(marshalerType)
}

func validateIntentPointer(value reflect.Value, depth int) error {
	if depth > maxIntentJSONDepth {
		return invalid("client intent nesting exceeds bounded depth")
	}
	if value.IsNil() {
		return nil
	}
	return validateIntentValue(value.Elem(), depth+1)
}

// validateIntentMap 校验 map key 与 value 的 UTF-8 和递归结构。
func validateIntentMap(value reflect.Value, depth int) error {
	if value.Len() > maxIntentObjectMembers {
		return invalid("client intent map has too many members")
	}
	for _, key := range value.MapKeys() {
		if key.Kind() == reflect.String && !utf8.ValidString(key.String()) {
			return invalid("client intent contains invalid UTF-8 map key")
		}
		if err := validateIntentValue(value.MapIndex(key), depth+1); err != nil {
			return err
		}
	}
	return nil
}

// validateIntentStruct 校验结构体字段的递归 intent 值。
func validateIntentStruct(value reflect.Value, depth int) error {
	if value.NumField() > maxIntentObjectMembers {
		return invalid("client intent struct has too many fields")
	}
	for index := 0; index < value.NumField(); index++ {
		if err := validateIntentValue(value.Field(index), depth+1); err != nil {
			return err
		}
	}
	return nil
}

// validateIntentSequence 校验数组或 slice 的递归 intent 值。
func validateIntentSequence(value reflect.Value, depth int) error {
	if value.Len() > maxIntentObjectMembers {
		return invalid("client intent sequence has too many members")
	}
	for index := 0; index < value.Len(); index++ {
		if err := validateIntentValue(value.Index(index), depth+1); err != nil {
			return err
		}
	}
	return nil
}

func (o TypedObject) validate() error {
	if o.Kind != ObjectKind || o.Version != SchemaVersion {
		return invalid("object kind or version is unsupported")
	}
	return validateDigest("object.digest", o.Digest)
}

func (s Scope) validate() error {
	if s.Kind != ScopeKind || s.Version != SchemaVersion {
		return invalid("scope kind or version is unsupported")
	}
	if err := validateText("scope.ref", s.Ref, 256); err != nil {
		return err
	}
	return validateDigest("scope.digest", s.Digest)
}

func (f TurnFence) validate(prefix string) error {
	if err := validateText(prefix+".turn_id", f.TurnID, 256); err != nil {
		return err
	}
	if f.TurnGeneration <= 0 {
		return invalid(prefix + ".turn_generation must be positive")
	}
	return validateDigest(prefix+".turn_digest", f.TurnDigest)
}

func (f EndpointFence) validate(prefix string) error {
	if err := validateExecutionFence(prefix+".execution", f.Execution); err != nil {
		return err
	}
	return f.Turn.validate(prefix)
}

func (p ParentCommand) validate() error {
	if err := validateText("parent.command_id", p.CommandID, 256); err != nil {
		return err
	}
	if p.CommandVersion != CommandVersion {
		return invalid("parent.command_version must equal 1")
	}
	if err := validateDigest("parent.intent_digest", p.IntentDigest); err != nil {
		return err
	}
	return validateDigest("parent.fence_digest", p.FenceDigest)
}

func (c MessageCommand) validate(parent ParentCommand, name string) error {
	return validateCommand(commandProjection{CommandID: c.CommandID, CommandVersion: c.CommandVersion, ParentCommandID: c.ParentCommandID, ParentCommandVersion: c.ParentCommandVersion, IntentDigest: c.IntentDigest, IdempotencyNamespace: c.IdempotencyNamespace, IdempotencyKeyDigest: c.IdempotencyKeyDigest, FenceDigest: c.FenceDigest}, parent, name)
}

func (c ReplyCommand) validate(parent ParentCommand, name string) error {
	return validateCommand(commandProjection{CommandID: c.CommandID, CommandVersion: c.CommandVersion, ParentCommandID: c.ParentCommandID, ParentCommandVersion: c.ParentCommandVersion, IntentDigest: c.IntentDigest, IdempotencyNamespace: c.IdempotencyNamespace, IdempotencyKeyDigest: c.IdempotencyKeyDigest, FenceDigest: c.FenceDigest}, parent, name)
}

// commandProjection 是 message/reply 共用的内部校验投影。
type commandProjection struct {
	CommandID, ParentCommandID, IntentDigest, IdempotencyNamespace, IdempotencyKeyDigest, FenceDigest string
	CommandVersion, ParentCommandVersion                                                              int64
}

// validateCommand 校验 child command 的字段完整性及其 parent 复用关系。
func validateCommand(c commandProjection, parent ParentCommand, name string) error {
	if err := validateCommandText(c, name); err != nil {
		return err
	}
	if err := validateCommandDigests(c, name); err != nil {
		return err
	}
	return validateCommandParent(c, parent, name)
}

// validateCommandText 校验 command 和幂等命名空间的有界文本。
func validateCommandText(c commandProjection, name string) error {
	if err := validateText(name+".command_id", c.CommandID, 256); err != nil {
		return err
	}
	return validateText(name+".parent_command_id", c.ParentCommandID, 256)
}

// validateCommandDigests 校验 command 的 intent、fence 和幂等 key 摘要。
func validateCommandDigests(c commandProjection, name string) error {
	if err := validateDigest(name+".intent_digest", c.IntentDigest); err != nil {
		return err
	}
	if err := validateText(name+".idempotency_namespace", c.IdempotencyNamespace, 256); err != nil {
		return err
	}
	if err := validateDigest(name+".fence_digest", c.FenceDigest); err != nil {
		return err
	}
	return validateDigest(name+".idempotency_key_digest", c.IdempotencyKeyDigest)
}

// validateCommandParent 校验 command 版本以及 parent command 的逐字段复用。
func validateCommandParent(c commandProjection, parent ParentCommand, name string) error {
	if c.CommandVersion != CommandVersion || c.ParentCommandVersion != parent.CommandVersion {
		return invalid(name + " command version is unsupported")
	}
	if c.ParentCommandID != parent.CommandID || c.IntentDigest != parent.IntentDigest || c.FenceDigest != parent.FenceDigest {
		return fmt.Errorf("%w: %s does not match parent", ErrAssociationConflict, name)
	}
	return nil
}

func (c ContentRef) validate(name string) error {
	if err := validateText(name+".ref", c.Ref, 512); err != nil {
		return err
	}
	if c.Version <= 0 {
		return invalid(name + ".version must be positive")
	}
	return validateDigest(name+".digest", c.Digest)
}

// validateExecutionFence 校验 execution/incarnation fence 的文本和代际。
func validateExecutionFence(name string, f common.ExecutionFence) error {
	for field, value := range map[string]string{name + ".agent_id": f.AgentID, name + ".reservation_id": f.ReservationID, name + ".incarnation_id": f.IncarnationID, name + ".source_fence_ref": f.SourceFenceRef} {
		if err := validateText(field, value, 256); err != nil {
			return err
		}
	}
	if f.ReservationGeneration <= 0 || f.IncarnationGeneration <= 0 || f.BindingVersion <= 0 {
		return invalid(name + " generations and binding version must be positive")
	}
	return validateDigest(name+".binding_digest", f.BindingDigest)
}

// validateText 校验字符串为有界、无控制字符的 canonical UTF-8 文本。
func validateText(name, value string, limit int) error {
	if value == "" || len(value) > limit || strings.TrimSpace(value) != value || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return invalid(name + " must be bounded canonical text")
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return invalid(name + " must not contain control characters")
		}
	}
	return nil
}

func validateDigest(name, value string) error {
	if len(value) != 64 || strings.ToLower(value) != value {
		return invalid(name + " must be lowercase sha-256 hex")
	}
	if _, err := hex.DecodeString(value); err != nil {
		return invalid(name + " must be lowercase sha-256 hex")
	}
	return nil
}

func invalid(reason string) error { return fmt.Errorf("%w: %s", ErrInvalidAssociation, reason) }
