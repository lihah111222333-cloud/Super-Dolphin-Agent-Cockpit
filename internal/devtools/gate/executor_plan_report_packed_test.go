package gate

import (
	"slices"
	"strings"
	"testing"
)

// TestPackedPlanGateDecoderRejectsUnknownDuplicateAndMissingSequence rejects malformed packed identities before projection.
func TestPackedPlanGateDecoderRejectsUnknownDuplicateAndMissingSequence(t *testing.T) {
	canonical := packedCanonicalRecords(t)
	for _, mutation := range packedMalformedMutations() {
		t.Run(mutation.name, func(t *testing.T) {
			records := slices.Clone(canonical)
			mutation.mutate(records)
			if _, err := decodePlanReportRecords(records); err == nil {
				t.Fatal("packed report decoder accepted malformed identity or sequence")
			}
		})
	}
}

type packedReportMutation struct {
	name   string
	mutate func([]planReportRecord)
}

// packedCanonicalRecords builds a parsed, valid mixed-provider report for mutation tests.
func packedCanonicalRecords(t *testing.T) []planReportRecord {
	t.Helper()
	report, _ := fourHundredElevenArchtestAndCodexappReport(t)
	chunks, err := EncodePlanExecutionReportChunks(report)
	if err != nil {
		t.Fatal(err)
	}
	recordLimit, err := planExecutionReportRecordLimit(len(report.Gates), len(report.CompileGroupExecutions))
	if err != nil {
		t.Fatal(err)
	}
	records, _, _, err := parsePlanReportRecords(chunks, recordLimit)
	if err != nil {
		t.Fatal(err)
	}
	return records
}

// packedMalformedMutations enumerates unknown-kind, duplicate-identity, and missing-sequence cases.
func packedMalformedMutations() []packedReportMutation {
	return []packedReportMutation{
		{name: "unknown packed kind", mutate: mutateUnknownPackedKind},
		{name: "duplicate dictionary identity", mutate: mutateDuplicateDictionaryIdentity},
		{name: "missing packed sequence", mutate: mutateMissingPackedSequence},
		{name: "empty first log fragment", mutate: mutateEmptyFirstLogFragment},
	}
}

func mutateUnknownPackedKind(records []planReportRecord) {
	index := firstPackedRecordIndex(records)
	if index >= 0 {
		records[index].kind = "?"
	}
}

func mutateDuplicateDictionaryIdentity(records []planReportRecord) {
	index := firstPlanReportRecordIndex(records, planReportGateDictionaryRecord)
	if index < 0 {
		return
	}
	fields := strings.Fields(records[index].payload)
	if len(fields) >= 5 {
		fields[4] = fields[3]
		records[index].payload = strings.Join(fields, " ")
	}
}

func mutateMissingPackedSequence(records []planReportRecord) {
	index := firstPackedRecordIndex(records)
	if index < 0 {
		return
	}
	fields := strings.SplitN(records[index].payload, " ", 2)
	if len(fields) == 2 {
		fields[0] = "000002"
		records[index].payload = strings.Join(fields, " ")
	}
}

func mutateEmptyFirstLogFragment(records []planReportRecord) {
	index := firstPackedRecordIndex(records)
	if index < 0 {
		return
	}
	fields := strings.SplitN(records[index].payload, " ", 15)
	if len(fields) == 15 && fields[12] != "000000" {
		fields[14] = ""
		records[index].payload = strings.Join(fields, " ")
	}
}

// firstPackedRecordIndex returns the first packed gate record in a parsed report.
func firstPackedRecordIndex(records []planReportRecord) int {
	return firstPlanReportRecordIndex(records, planReportPackedGateRecord)
}

// firstPlanReportRecordIndex locates one record kind and fails closed if the fixture is incomplete.
func firstPlanReportRecordIndex(records []planReportRecord, kind string) int {
	for index, record := range records {
		if record.kind == kind {
			return index
		}
	}
	return -1
}
