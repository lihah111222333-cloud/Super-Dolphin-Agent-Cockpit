package sqliteauthority

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
)

type valueRow []any

// Scan 按生产扫描顺序把测试值写入真实 mapper 目标。
func (r valueRow) Scan(destinations ...any) error {
	if len(destinations) != len(r) {
		return fmt.Errorf("scan destination count = %d, want %d", len(destinations), len(r))
	}
	for index, destination := range destinations {
		reflect.ValueOf(destination).Elem().Set(reflect.ValueOf(r[index]))
	}
	return nil
}

// TestRowIntegrityFieldCoverage 动态枚举生产 DTO，并拒绝 mapper 登记缺失或陈旧。
func TestRowIntegrityFieldCoverage(t *testing.T) {
	assertFieldCoverage(t, reflect.TypeOf(recoverycustody.SubmissionRequest{}), "SubmissionRequest", submissionRequestCoverage())
	assertFieldCoverage(t, reflect.TypeOf(recoverycustody.SubmissionHandle{}), "SubmissionHandle", submissionHandleCoverage())
	assertFieldCoverage(t, reflect.TypeOf(ExpectedResolution{}), "ExpectedResolution", resolutionCoverage())
	assertFieldCoverage(t, reflect.TypeOf(FinalizationRequest{}), "FinalizationRequest", finalizationRequestCoverage())
	assertFieldCoverage(t, reflect.TypeOf(recoverycustody.ExternalReceipt{}), "ExternalReceipt", receiptCoverage())
}

// TestRowIntegrityScannersRejectScalarJSONDrift 对每个生产字段执行 one-hot 漂移验证。
func TestRowIntegrityScannersRejectScalarJSONDrift(t *testing.T) {
	t.Run("submission", testSubmissionScannerDrift)
	t.Run("resolution", testResolutionScannerDrift)
	t.Run("terminal_receipt", testTerminalReceiptScannerDrift)
}

// TestTerminalReceiptJoinRelationsRejectDrift 覆盖终态四张历史表的关系字段漂移。
func TestTerminalReceiptJoinRelationsRejectDrift(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*terminalReceiptRow)
	}{
		{name: "submission_status", mutate: func(row *terminalReceiptRow) { row.SubmissionStatus = recoverycustody.StatusAborted }},
		{name: "submission_digest", mutate: func(row *terminalReceiptRow) { row.SubmissionReceiptDigest.String += "x" }},
		{name: "resolution_state", mutate: func(row *terminalReceiptRow) { row.Resolution.Stored.State = resolutionStateAnchored }},
		{name: "resolution_kind", mutate: func(row *terminalReceiptRow) { row.Resolution.Stored.Expected.Kind = recoverycustody.ReceiptAbort }},
		{name: "resolution_operation", mutate: func(row *terminalReceiptRow) { row.Resolution.OperationID += "x" }},
		{name: "checkpoint_generation", mutate: func(row *terminalReceiptRow) { row.CheckpointGeneration++ }},
		{name: "checkpoint_anchor", mutate: func(row *terminalReceiptRow) { row.CheckpointAnchorDigest = schemaTestDigest("c") }},
		{name: "checkpoint_result_root", mutate: func(row *terminalReceiptRow) { row.CheckpointResultHeadRootDigest = schemaTestDigest("c") }},
		{name: "checkpoint_finalization", mutate: func(row *terminalReceiptRow) { row.CheckpointFinalizationDigest = schemaTestDigest("c") }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			row := terminalIntegrityFixture(t)
			test.mutate(&row)
			if _, err := scanTerminalReceipt(valueRow(scanValues(terminalReceiptScanTargets(&row)))); !errors.Is(err, ErrSchemaMismatch) {
				t.Fatalf("scanTerminalReceipt() error = %v, want ErrSchemaMismatch", err)
			}
		})
	}
}

// TestHistoricalTerminalReceiptSurvivesHeadAdvance 验证旧回执不错误绑定后续 current head。
func TestHistoricalTerminalReceiptSurvivesHeadAdvance(t *testing.T) {
	config := authorityTestConfig(t, "historical-terminal")
	if err := Bootstrap(t.Context(), config, acceptingLease{}); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	finalizer := sequentialEvidenceFinalizer{config: config}
	authority := openEvidenceTestAuthority(t, config, finalizer, acceptingReceiptVerifier{})
	firstRequest, firstTerminal := resolveDistinctTerminal(t, authority, "first")
	resolveDistinctTerminal(t, authority, "second")
	firstHandle, err := recoverycustody.NewSubmissionHandle(firstRequest)
	if err != nil {
		t.Fatalf("NewSubmissionHandle(first) error = %v", err)
	}
	queried, err := authority.Query(t.Context(), firstHandle)
	if err != nil || !reflect.DeepEqual(queried, firstTerminal) {
		t.Fatalf("Query(first historical terminal) = (%+v, %v), want %+v", queried, err, firstTerminal)
	}
}

// testSubmissionScannerDrift 验证 submission 的请求与句柄全部标量被真实 mapper 消费。
func testSubmissionScannerDrift(t *testing.T) {
	t.Helper()
	request := schemaTestRequest(t)
	handle, err := recoverycustody.NewSubmissionHandle(request)
	if err != nil {
		t.Fatalf("NewSubmissionHandle() error = %v", err)
	}
	requestJSON := mustJSON(t, request)
	handleJSON := mustJSON(t, handle)
	stored := storedSubmissionRow{Request: request, HandleDigest: handle.HandleDigest, RequestJSON: requestJSON,
		HandleJSON: handleJSON, Status: recoverycustody.StatusPending}
	values := scanValues(submissionScanTargets(&stored))
	coverage := mergeCoverage(submissionRequestCoverage(), submissionHandleCoverage())
	assertMappedDriftRejected(t, values, coverage, func(row valueRow) error {
		_, _, scanErr := scanSubmission(row)
		return scanErr
	})
}

// testResolutionScannerDrift 验证 resolution 标量、JSON 与 expected tuple 双向绑定。
func testResolutionScannerDrift(t *testing.T) {
	t.Helper()
	terminal := terminalIntegrityFixture(t)
	values := scanValues(resolutionScanTargets(&terminal.Resolution))
	assertMappedDriftRejected(t, values, resolutionStorageCoverage(), func(row valueRow) error {
		_, scanErr := scanStoredResolution(row)
		return scanErr
	})
}

// testTerminalReceiptScannerDrift 验证 receipt 全部标量都与 canonical JSON 精确绑定。
func testTerminalReceiptScannerDrift(t *testing.T) {
	t.Helper()
	terminal := terminalIntegrityFixture(t)
	values := scanValues(terminalReceiptScanTargets(&terminal))
	assertMappedDriftRejected(t, values, receiptCoverage(), func(row valueRow) error {
		_, scanErr := scanTerminalReceipt(row)
		return scanErr
	})
}

// assertMappedDriftRejected 先验证 GREEN，再逐列注入真实 mapper omission 等价漂移。
func assertMappedDriftRejected(t *testing.T, values []any, coverage map[string]int, scan func(valueRow) error) {
	t.Helper()
	if err := scan(valueRow(values)); err != nil {
		t.Fatalf("baseline scan error = %v", err)
	}
	for _, index := range uniqueIndexes(coverage) {
		mutated := append([]any(nil), values...)
		drifted, err := driftScalar(mutated[index])
		if err != nil {
			t.Fatal(err)
		}
		mutated[index] = drifted
		if err := scan(valueRow(mutated)); !errors.Is(err, ErrSchemaMismatch) {
			t.Fatalf("mapper scalar index %d drift error = %v, want ErrSchemaMismatch", index, err)
		}
	}
}

// assertFieldCoverage 比较反射生产字段与显式消费登记的精确差集。
func assertFieldCoverage(t *testing.T, producer reflect.Type, prefix string, coverage map[string]int) {
	t.Helper()
	want := reflectedFieldPaths(producer, prefix)
	got := make([]string, 0, len(coverage))
	for field := range coverage {
		got = append(got, field)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("field coverage = %v, want %v", got, want)
	}
}

// reflectedFieldPaths 递归枚举 DTO 结构字段，time.Time 保持为单个标量叶子。
func reflectedFieldPaths(producer reflect.Type, prefix string) []string {
	var fields []string
	for index := 0; index < producer.NumField(); index++ {
		field := producer.Field(index)
		path := prefix + "." + field.Name
		if field.Type.Kind() == reflect.Struct && field.Type.PkgPath() != "time" {
			fields = append(fields, reflectedFieldPaths(field.Type, path)...)
			continue
		}
		fields = append(fields, path)
	}
	sort.Strings(fields)
	return fields
}

// submissionRequestCoverage 登记 SubmissionRequest 标量到 SELECT/Scan 索引。
func submissionRequestCoverage() map[string]int {
	return map[string]int{"SubmissionRequest.SchemaVersion": 0, "SubmissionRequest.OperationID": 1,
		"SubmissionRequest.SecurityDomainID": 2, "SubmissionRequest.AuthorityProfile": 3,
		"SubmissionRequest.CommandID": 4, "SubmissionRequest.CommandVersion": 5,
		"SubmissionRequest.CommandProfileDigest": 6, "SubmissionRequest.AuthorizationDigest": 7,
		"SubmissionRequest.IdentityFenceDigest": 8, "SubmissionRequest.ContentSnapshotDigest": 9,
		"SubmissionRequest.ResourceSnapshotDigest": 10, "SubmissionRequest.BatchDigest": 11,
		"SubmissionRequest.IdempotencyKeyDigest": 12, "SubmissionRequest.RequestDigest": 13}
}

// submissionHandleCoverage 登记 SubmissionHandle 标量到 SELECT/Scan 索引。
func submissionHandleCoverage() map[string]int {
	return map[string]int{"SubmissionHandle.SchemaVersion": 0, "SubmissionHandle.OperationID": 1,
		"SubmissionHandle.SecurityDomainID": 2, "SubmissionHandle.AuthorityProfile": 3,
		"SubmissionHandle.CommandID": 4, "SubmissionHandle.CommandVersion": 5,
		"SubmissionHandle.RequestDigest": 13, "SubmissionHandle.BatchDigest": 11, "SubmissionHandle.HandleDigest": 14}
}

// resolutionCoverage 登记 ExpectedResolution 全部叶子到联表 SELECT/Scan 索引。
func resolutionCoverage() map[string]int {
	return map[string]int{"ExpectedResolution.ResolutionRequest.SchemaVersion": 0,
		"ExpectedResolution.ResolutionRequest.ResolutionID":            1,
		"ExpectedResolution.ResolutionRequest.Handle.SchemaVersion":    3,
		"ExpectedResolution.ResolutionRequest.Handle.OperationID":      4,
		"ExpectedResolution.ResolutionRequest.Handle.SecurityDomainID": 5,
		"ExpectedResolution.ResolutionRequest.Handle.AuthorityProfile": 6,
		"ExpectedResolution.ResolutionRequest.Handle.CommandID":        7,
		"ExpectedResolution.ResolutionRequest.Handle.CommandVersion":   8,
		"ExpectedResolution.ResolutionRequest.Handle.RequestDigest":    9,
		"ExpectedResolution.ResolutionRequest.Handle.BatchDigest":      10,
		"ExpectedResolution.ResolutionRequest.Handle.HandleDigest":     11,
		"ExpectedResolution.ResolutionRequest.Kind":                    12,
		"ExpectedResolution.ResolutionRequest.ResolutionRequestDigest": 13,
		"ExpectedResolution.ResolutionRequest.FinalizationOperationID": 14,
		"ExpectedResolution.ExpectedResolutionDigest":                  15,
		"ExpectedResolution.HeadID":                                    18,
		"ExpectedResolution.ExpectedHeadRevision":                      19,
		"ExpectedResolution.ExpectedHeadRootDigest":                    20,
		"ExpectedResolution.ExpectedAntiRollbackGeneration":            21,
		"ExpectedResolution.ExpectedAnchorDigest":                      22,
		"ExpectedResolution.SecurityTimeLineageDigest":                 23,
		"ExpectedResolution.ExpectedSecurityTimeGeneration":            24,
		"ExpectedResolution.ExpectedSecurityTimeRevision":              25}
}

// resolutionStorageCoverage 扩展 expected producer 登记，并覆盖最终摘要持久化列。
func resolutionStorageCoverage() map[string]int {
	coverage := resolutionCoverage()
	coverage["FinalizationRequest.FinalizationRequestDigest"] = 16
	return coverage
}

// finalizationRequestCoverage 把 expected mapper 登记提升到最终请求生产 DTO，并登记最终摘要列。
func finalizationRequestCoverage() map[string]int {
	coverage := make(map[string]int)
	for field, index := range resolutionCoverage() {
		name := strings.TrimPrefix(field, "ExpectedResolution.")
		coverage["FinalizationRequest.ExpectedResolution."+name] = index
	}
	coverage["FinalizationRequest.FinalizationRequestDigest"] = 16
	return coverage
}

// resolutionJSONCoverage 返回必须与 canonical JSON 双向相等的 ResolutionRequest 字段。
func resolutionJSONCoverage() map[string]int {
	all := resolutionCoverage()
	covered := make(map[string]int)
	for field, index := range all {
		if strings.HasPrefix(field, "ExpectedResolution.ResolutionRequest.") {
			covered[field] = index
		}
	}
	return covered
}

// receiptCoverage 登记 ExternalReceipt 全部叶子到终态联表 SELECT/Scan 索引。
func receiptCoverage() map[string]int {
	return map[string]int{"ExternalReceipt.OperationID": 0, "ExternalReceipt.SchemaVersion": 2,
		"ExternalReceipt.Kind": 3, "ExternalReceipt.ReceiptID": 4, "ExternalReceipt.AuthorityID": 5,
		"ExternalReceipt.AuthorityClusterID": 6, "ExternalReceipt.AuthorityProfile": 7,
		"ExternalReceipt.AuthorityGeneration": 8, "ExternalReceipt.OwnerID": 9,
		"ExternalReceipt.OwnerGeneration": 10, "ExternalReceipt.AuthorityProfileGeneration": 11,
		"ExternalReceipt.AuthorityProfileDigest": 12, "ExternalReceipt.SecurityDomainID": 13,
		"ExternalReceipt.HeadID": 14, "ExternalReceipt.HeadRevision": 15,
		"ExternalReceipt.HeadRootDigest": 16, "ExternalReceipt.RequestDigest": 17,
		"ExternalReceipt.BatchDigest": 18, "ExternalReceipt.SecurityTimeLineageDigest": 19,
		"ExternalReceipt.SecurityTimeGeneration": 20, "ExternalReceipt.SecurityTimeRevision": 21,
		"ExternalReceipt.AntiRollbackGeneration": 22, "ExternalReceipt.AntiRollbackAnchorDigest": 23,
		"ExternalReceipt.QuorumAttestationDigest": 24, "ExternalReceipt.QuorumAttestationRef": 25,
		"ExternalReceipt.SignatureDigest": 26, "ExternalReceipt.SignatureRef": 27,
		"ExternalReceipt.AuthorityTime": 28, "ExternalReceipt.RetentionPolicyDigest": 29,
		"ExternalReceipt.RetentionUntil": 30, "ExternalReceipt.GCNotBeforeAnchorGeneration": 31,
		"ExternalReceipt.ReceiptDigest": 32}
}

// terminalIntegrityFixture 构造经过完整验证的终态联表扫描行。
func terminalIntegrityFixture(t *testing.T) terminalReceiptRow {
	t.Helper()
	config := authorityTestConfig(t, "row-integrity")
	request := schemaTestRequest(t)
	handle, err := recoverycustody.NewSubmissionHandle(request)
	if err != nil {
		t.Fatalf("NewSubmissionHandle() error = %v", err)
	}
	resolution := finalizedResolutionRequest(t, handle, "row-integrity", recoverycustody.ReceiptAvailability)
	expected, err := BindExpectedResolutionDigest(ExpectedResolution{ResolutionRequest: resolution, HeadID: config.HeadID,
		ExpectedHeadRevision: 0, ExpectedHeadRootDigest: config.InitialHeadRootDigest,
		ExpectedAntiRollbackGeneration: config.AntiRollbackGeneration, ExpectedAnchorDigest: config.AntiRollbackAnchorDigest,
		SecurityTimeLineageDigest:      config.SecurityTimeLineageDigest,
		ExpectedSecurityTimeGeneration: config.SecurityTimeGeneration,
		ExpectedSecurityTimeRevision:   config.SecurityTimeRevision})
	if err != nil {
		t.Fatalf("BindExpectedResolutionDigest() error = %v", err)
	}
	evidence, err := finalizedEvidenceFixture(config, expected)
	if err != nil {
		t.Fatalf("finalizedEvidenceFixture() error = %v", err)
	}
	return terminalRowFromEvidence(t, expected, evidence)
}

// terminalRowFromEvidence 把 expected/evidence 展开成真实终态 SELECT 的全部列。
func terminalRowFromEvidence(t *testing.T, expected ExpectedResolution, evidence FinalizedEvidence) terminalReceiptRow {
	t.Helper()
	r := evidence.Receipt
	c := evidence.Checkpoint
	finalization, err := BindFinalizationRequest(expected)
	if err != nil {
		t.Fatalf("BindFinalizationRequest() error = %v", err)
	}
	return terminalReceiptRow{Receipt: r, ResolutionID: expected.ResolutionID, AuthorityTime: canonicalTime(r.AuthorityTime),
		RetentionUntil: canonicalTime(r.RetentionUntil), ReceiptJSON: mustJSON(t, r),
		ReceiptCheckpointGeneration: c.Generation, SubmissionStatus: recoverycustody.StatusAvailable,
		SubmissionReceiptDigest: sql.NullString{String: r.ReceiptDigest, Valid: true},
		Resolution: storedResolutionRow{Stored: storedResolution{Expected: expected, State: resolutionStateTerminal},
			OperationID: r.OperationID, FinalizationRequestDigest: finalization.FinalizationRequestDigest,
			RequestJSON: mustJSON(t, expected.ResolutionRequest)},
		ResolutionCheckpointGeneration: sql.NullInt64{Int64: c.Generation, Valid: true},
		ResolutionCheckpointAnchor:     sql.NullString{String: c.AnchorDigest, Valid: true},
		ResolutionResultHeadRoot:       sql.NullString{String: c.ResultHeadRootDigest, Valid: true},
		ResolutionFinalizationDigest:   sql.NullString{String: c.FinalizationDigest, Valid: true},
		CheckpointGeneration:           c.Generation, CheckpointPredecessorGeneration: c.PredecessorGeneration,
		CheckpointPredecessorAnchorDigest: c.PredecessorAnchorDigest, CheckpointAnchorDigest: c.AnchorDigest,
		CheckpointResultHeadRootDigest: c.ResultHeadRootDigest, CheckpointFinalizationDigest: c.FinalizationDigest}
}

// scanValues 把 mapper 目标当前值快照成可重放行。
func scanValues(targets []any) []any {
	values := make([]any, len(targets))
	for index, target := range targets {
		values[index] = reflect.ValueOf(target).Elem().Interface()
	}
	return values
}

// mergeCoverage 合并同一扫描器的多组生产字段登记。
func mergeCoverage(groups ...map[string]int) map[string]int {
	merged := make(map[string]int)
	for _, group := range groups {
		for field, index := range group {
			merged[field] = index
		}
	}
	return merged
}

// uniqueIndexes 返回排序后的唯一 mapper 索引。
func uniqueIndexes(coverage map[string]int) []int {
	seen := make(map[int]struct{})
	for _, index := range coverage {
		seen[index] = struct{}{}
	}
	indexes := make([]int, 0, len(seen))
	for index := range seen {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	return indexes
}

// driftScalar 保持 SQLite 扫描类型不变并注入 one-hot 标量漂移。
func driftScalar(value any) (any, error) {
	switch typed := value.(type) {
	case string:
		return typed + "x", nil
	case int64:
		return typed + 1, nil
	default:
		return nil, fmt.Errorf("unsupported drift scalar %T", value)
	}
}

// mustJSON 生成测试所需的 canonical JSON。
func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return string(encoded)
}

type sequentialEvidenceFinalizer struct{ config Config }

// Finalize 为连续 head 生成互不冲突且与 expected 绑定的终态证据。
func (f sequentialEvidenceFinalizer) Finalize(_ context.Context, request FinalizationRequest) (FinalizedEvidence, error) {
	return f.evidence(request.ExpectedResolution)
}

// Query 重放与同一 expected 精确绑定的终态证据。
func (f sequentialEvidenceFinalizer) Query(_ context.Context, request FinalizationRequest) (FinalizationObservation, error) {
	return finalizationObservationFixture(request, FinalizationQueryAbsent, nil), nil
}

// evidence 基于代际派生唯一 checkpoint/receipt，避免跨 head 测试夹具冲突。
func (f sequentialEvidenceFinalizer) evidence(expected ExpectedResolution) (FinalizedEvidence, error) {
	evidence, err := finalizedEvidenceFixture(f.config, expected)
	if err != nil {
		return FinalizedEvidence{}, err
	}
	anchorDigit := strconv.FormatInt((expected.ExpectedAntiRollbackGeneration%6)+8, 16)
	rootDigit := strconv.FormatInt((expected.ExpectedAntiRollbackGeneration%6)+1, 16)
	evidence.Checkpoint.AnchorDigest = schemaTestDigest(anchorDigit)
	evidence.Checkpoint.AnchorRef = "anchor://checkpoint/" + evidence.Checkpoint.AnchorDigest
	evidence.Checkpoint.ResultHeadRootDigest = schemaTestDigest(rootDigit)
	evidence.Receipt.ReceiptID = "receipt-" + expected.ResolutionID
	evidence.Receipt.AntiRollbackAnchorDigest = evidence.Checkpoint.AnchorDigest
	evidence.Receipt.HeadRootDigest = evidence.Checkpoint.ResultHeadRootDigest
	receipt, err := recoverycustody.FinalizeReceiptDigest(evidence.Receipt)
	if err != nil {
		return FinalizedEvidence{}, err
	}
	evidence.Receipt = receipt
	return evidence, nil
}

// resolveDistinctTerminal 提交并终态化一个唯一 operation。
func resolveDistinctTerminal(t *testing.T, authority *Authority, identity string) (recoverycustody.SubmissionRequest, recoverycustody.Outcome) {
	t.Helper()
	request := schemaTestRequest(t)
	request.OperationID = "operation-" + identity
	request.IdempotencyKeyDigest = schemaTestDigest(distinctHexDigit(identity))
	request, err := recoverycustody.FinalizeRequestDigest(request)
	if err != nil {
		t.Fatalf("FinalizeRequestDigest(%s) error = %v", identity, err)
	}
	pending, err := authority.Submit(t.Context(), request)
	if err != nil {
		t.Fatalf("Submit(%s) error = %v", identity, err)
	}
	resolution := finalizedResolutionRequest(t, pending.Handle, identity, recoverycustody.ReceiptAvailability)
	terminal, err := authority.Resolve(t.Context(), resolution)
	if err != nil {
		t.Fatalf("Resolve(%s) error = %v", identity, err)
	}
	return request, terminal
}

// distinctHexDigit 为顺序夹具返回稳定且合法的十六进制身份。
func distinctHexDigit(identity string) string {
	if identity == "first" {
		return "d"
	}
	return "e"
}
