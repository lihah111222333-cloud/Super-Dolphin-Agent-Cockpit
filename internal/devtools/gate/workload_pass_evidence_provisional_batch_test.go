package gate

import (
	"reflect"
	"testing"
)

// TestProvisionalWorkloadProjectionBatchMatchesCanonicalReadback 锁定失败运行的
// 批量 projection 与写入 evidence 时的单项 canonical readback 完全一致。
func TestProvisionalWorkloadProjectionBatchMatchesCanonicalReadback(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, _, _ := recordWorkloadPassRun(t, store, "failed-batch-projection", 1, "failed-batch-projection-workload")
	record.Status, record.Authoritative = ResultStatusFailed, false
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		t.Fatalf("record cleaned failed run: %v", err)
	}
	database := openWorkloadPassDatabase(t, store)
	defer database.Close()
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	canonical, err := loadRemoteCIRunRow(transaction, record.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if err := loadRemoteCIRunDetails(transaction, record.JobID, &canonical); err != nil {
		t.Fatal(err)
	}
	batch, err := loadRetainedConsumerChunk(transaction, []string{record.JobID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := batch[record.JobID]; !reflect.DeepEqual(got, canonical) {
		canonicalValue, batchValue := reflect.ValueOf(canonical), reflect.ValueOf(got)
		var fields []string
		for index := 0; index < canonicalValue.NumField(); index++ {
			if !reflect.DeepEqual(canonicalValue.Field(index).Interface(), batchValue.Field(index).Interface()) {
				fields = append(fields, canonicalValue.Type().Field(index).Name)
			}
		}
		t.Fatalf("batch projection differs in fields %v", fields)
	}
}
