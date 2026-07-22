package localci

import (
	"reflect"
	"strings"
	"testing"
)

type schedulerTransportJSONCase struct {
	name  string
	value any
	want  map[string]string
}

func TestSchedulerTransportDTOJSONFields(t *testing.T) {
	t.Parallel()
	cases := append(schedulerPublicDTOJSONCases(), schedulerWireDTOJSONCases()...)
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			assertSchedulerTransportJSONFields(t, testCase.value, testCase.want)
		})
	}
}

func schedulerPublicDTOJSONCases() []schedulerTransportJSONCase {
	return []schedulerTransportJSONCase{
		{
			name: "WorkloadRequest", value: WorkloadRequest{},
			want: map[string]string{
				"ID": "id", "InvocationID": "invocation_id", "EnqueueSequence": "enqueue_sequence",
				"Subsequence": "subsequence", "Kind": "kind", "ServiceCount": "service_count",
				"GroupIdentity": "group_identity", "GroupSize": "group_size",
				"ShardIdentities": "shard_identities",
				"Dependencies":    "dependencies",
			},
		},
		{name: "Lease", value: Lease{}, want: map[string]string{
			"ID": "id", "WorkloadID": "workload_id", "Kind": "kind",
			"GroupIdentity": "group_identity", "ShardIdentity": "shard_identity",
		}},
		{name: "WorkloadReservation", value: WorkloadReservation{}, want: map[string]string{
			"WorkloadID": "workload_id", "GroupIdentity": "group_identity", "Leases": "leases",
		}},
		{name: "WorkloadSnapshot", value: WorkloadSnapshot{}, want: map[string]string{
			"Request": "request", "Status": "status",
		}},
		{name: "SchedulerSnapshot", value: SchedulerSnapshot{}, want: map[string]string{
			"Workloads": "workloads", "Leases": "leases",
		}},
	}
}

func schedulerWireDTOJSONCases() []schedulerTransportJSONCase {
	return []schedulerTransportJSONCase{
		{name: "schedulerWireRequest", value: schedulerWireRequest{}, want: map[string]string{
			"Version": "version", "RequestID": "request_id", "DaemonKey": "daemon_key",
			"Method": "method", "Params": "params",
		}},
		{name: "schedulerWireResponse", value: schedulerWireResponse{}, want: map[string]string{
			"Version": "version", "RequestID": "request_id", "Result": "result", "Error": "error",
		}},
		{name: "schedulerWireError", value: schedulerWireError{}, want: map[string]string{
			"Code": "code", "Message": "message",
		}},
		{name: "schedulerEnqueueParams", value: schedulerEnqueueParams{}, want: map[string]string{
			"Request": "request",
		}},
		{name: "schedulerCompleteParams", value: schedulerCompleteParams{}, want: map[string]string{
			"WorkloadID": "workload_id", "Status": "status",
		}},
		{name: "schedulerGroupParams", value: schedulerGroupParams{}, want: map[string]string{
			"WorkloadID": "workload_id", "GroupIdentity": "group_identity", "Status": "status",
		}},
		{name: "schedulerShardFailureParams", value: schedulerShardFailureParams{}, want: map[string]string{
			"WorkloadID": "workload_id", "GroupIdentity": "group_identity", "ShardIdentity": "shard_identity",
		}},
		{name: "schedulerShardFailureResult", value: schedulerShardFailureResult{}, want: map[string]string{
			"CancelShardIdentities": "cancel_shard_identities",
		}},
		{name: "schedulerStateParams", value: schedulerStateParams{}, want: map[string]string{
			"WorkloadID": "workload_id",
		}},
		{name: "schedulerReserveResult", value: schedulerReserveResult{}, want: map[string]string{
			"Reservations": "reservations",
		}},
		{name: "schedulerStateResult", value: schedulerStateResult{}, want: map[string]string{
			"Status": "status",
		}},
		{name: "schedulerSnapshotResult", value: schedulerSnapshotResult{}, want: map[string]string{
			"Snapshot": "snapshot",
		}},
	}
}

func assertSchedulerTransportJSONFields(t *testing.T, value any, want map[string]string) {
	t.Helper()
	typeOf := reflect.TypeOf(value)
	seen := make(map[string]string, typeOf.NumField())
	for index := range typeOf.NumField() {
		field := typeOf.Field(index)
		if !field.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			t.Errorf("%s.%s has no transport JSON field", typeOf.Name(), field.Name)
			continue
		}
		if previous, exists := seen[name]; exists {
			t.Errorf("%s JSON field %q is shared by %s and %s", typeOf.Name(), name, previous, field.Name)
		}
		seen[name] = field.Name
		if expected, exists := want[field.Name]; !exists {
			t.Errorf("%s.%s is missing from transport field registry", typeOf.Name(), field.Name)
		} else if name != expected {
			t.Errorf("%s.%s JSON field=%q want=%q", typeOf.Name(), field.Name, name, expected)
		}
	}
	for field := range want {
		if _, exists := typeOf.FieldByName(field); !exists {
			t.Errorf("%s transport field registry contains stale field %s", typeOf.Name(), field)
		}
	}
}
