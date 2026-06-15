//go:build legacy_pg_fake

package taskdag

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

// F1.5 / ADR-009 鈥?RecordNodeSpawn 鍐欏叆鍥炶矾鍗曟祴銆?
//
// 鐢ㄤ緥瑕嗙洊锛?
//   1. 棣栨 spawn锛歴pawning_thread_id 浠庣┖鍐欏叆 thread-1锛屼笉 append events
//      锛堝巻鍙茬┖銆佹棤鍙繚鐣欑殑 prev锛夈€?
//   2. 閲嶈瘯 spawn锛歴pawning_thread_id 浠?thread-1 瑕嗙洊涓?thread-2锛屼笖鍚屼簨鍔″唴
//      缁?dag_key 褰撳墠 running run 鐨?events 鏁扮粍 append 涓€鏉?node_spawn 鍘嗗彶銆?
//   3. 閲嶈瘯 + 鏃?running run锛氳鐩栧彂鐢熶絾 events append silently miss
//      锛圓ppendedEvent=false锛夛紝涓嶈涓洪敊璇紙spawn 鍘嗗彶鏄緟鍔╋紝缂哄け涓嶅簲鐮村潖 spawn 涓昏矾锛夈€?
//   4. 鍏ュ弬闃插尽锛氱┖ thread_id 鎷掔粷锛涚┖ dag/node_key 鎷掔粷銆?

// newSpawnRecorderTestStore 鍖呰 newTaskDAGTestStore 鐨勮仛鍚?Store 杩斿洖鍊硷紝鎻愬彇 narrow
// port NodeSpawnRecorderStore銆侳1.5 鍚庣画淇锛氫粠鑱氬悎 Store 涓媶鍑?NodeSpawnRecorderStore
// 浠ヨ繃 archtest TestInterfaceIsolationBudgets锛屾墍浠ユ祴璇曚笉鑳藉啀鐩存帴杩?Store 鎺ュ彛璋冦€?
func newSpawnRecorderTestStore() (NodeSpawnRecorderStore, *fakeTaskDAGDB, time.Time) {
	s, db, now := newTaskDAGTestStore()
	return s.(NodeSpawnRecorderStore), db, now
}

func TestRecordNodeSpawn_FirstSpawn_WritesFieldNoEvents(t *testing.T) {
	store, db, now := newSpawnRecorderTestStore()
	runID := seedRunID(db, "dag-1", "run-A")
	seedRuntimeNode(t, db, now, runID, "n1", nil, "running", "agent-a")

	res, err := store.RecordNodeSpawn(context.Background(), RecordNodeSpawnInput{
		DagKey:   "dag-1",
		NodeKey:  "n1",
		RunID:    runID,
		ThreadID: "thread-1",
	})
	if err != nil {
		t.Fatalf("RecordNodeSpawn first error = %v", err)
	}
	if res == nil || res.Node == nil {
		t.Fatalf("RecordNodeSpawn first returned nil node: %+v", res)
	}
	if got := stringValue(res.Node.SpawningThreadID); got != "thread-1" {
		t.Fatalf("Node.SpawningThreadID = %q, want thread-1", got)
	}
	if res.PreviousThreadID != "" {
		t.Fatalf("PreviousThreadID = %q, want empty on first spawn", res.PreviousThreadID)
	}
	if res.AppendedEvent {
		t.Fatalf("AppendedEvent = true, want false on first spawn (no history yet)")
	}
	// Run.events should remain empty since first spawn doesn't write history.
	run := db.runs["run-A"]
	if len(run.Events) != 0 {
		t.Fatalf("run.events = %q, want empty on first spawn", string(run.Events))
	}
}

func TestRecordNodeSpawn_RetryOverwrite_AppendsEvent(t *testing.T) {
	store, db, now := newSpawnRecorderTestStore()
	runID := seedRunID(db, "dag-1", "run-A")
	seedRuntimeNode(t, db, now, runID, "n1", nil, "running", "agent-a")

	// 鍏堝啓涓€閬?thread-1锛堥娆?spawn锛夈€?
	mustRecordNodeSpawn(t, store, runID, "thread-1", "first spawn")

	// 鍐嶅啓 thread-2 鈥斺€?閲嶈瘯鍦烘櫙锛氭棫 thread-1 搴旇繘 events 鍘嗗彶銆?
	res := mustRecordNodeSpawn(t, store, runID, "thread-2", "retry spawn")
	assertRetrySpawnResult(t, res, "thread-2", "thread-1", "run-A")

	// 鏍￠獙 events 涓甫浜嗕竴鏉?node_spawn 鍘嗗彶锛坧rev_thread_id=thread-1, thread_id=thread-2锛夈€?
	assertSingleNodeSpawnEvent(t, db.runs["run-A"].Events, "n1", "thread-1", "thread-2")
}

func mustRecordNodeSpawn(t *testing.T, store NodeSpawnRecorderStore, runID int64, threadID, label string) *RecordNodeSpawnResult {
	t.Helper()
	res, err := store.RecordNodeSpawn(context.Background(), RecordNodeSpawnInput{
		DagKey: "dag-1", NodeKey: "n1", RunID: runID, ThreadID: threadID,
	})
	if err != nil {
		t.Fatalf("%s error = %v", label, err)
	}
	return res
}

func assertRetrySpawnResult(t *testing.T, res *RecordNodeSpawnResult, wantThread, wantPrev, wantRun string) {
	t.Helper()
	if got := stringValue(res.Node.SpawningThreadID); got != wantThread {
		t.Fatalf("Node.SpawningThreadID = %q, want %s after retry", got, wantThread)
	}
	if res.PreviousThreadID != wantPrev {
		t.Fatalf("PreviousThreadID = %q, want %s", res.PreviousThreadID, wantPrev)
	}
	if !res.AppendedEvent {
		t.Fatalf("AppendedEvent = false, want true on retry overwrite")
	}
	if res.RunKey != wantRun {
		t.Fatalf("RunKey = %q, want %s", res.RunKey, wantRun)
	}
}

func assertSingleNodeSpawnEvent(t *testing.T, raw json.RawMessage, wantNode, wantPrev, wantThread string) {
	t.Helper()
	if len(raw) == 0 {
		t.Fatalf("run.events empty after retry, expected one node_spawn entry")
	}
	var arr []nodeSpawnEvent
	if err := json.Unmarshal(raw, &arr); err != nil {
		t.Fatalf("unmarshal events %q: %v", string(raw), err)
	}
	if len(arr) != 1 {
		t.Fatalf("events len = %d, want 1; raw = %s", len(arr), string(raw))
	}
	assertNodeSpawnEvent(t, arr[0], wantNode, wantPrev, wantThread)
}

func TestRecordNodeSpawn_RetryWithoutRunningRunFailsFast(t *testing.T) {
	store, db, now := newSpawnRecorderTestStore()
	const runID int64 = 101
	seedRuntimeNode(t, db, now, runID, "n1", nil, "running", "agent-a")
	// 鏁呮剰涓?seedRun 鈥斺€?妯℃嫙銆孌AG 鏈夎妭鐐逛絾娌℃湁 running run銆嶏紙M3 涔嬪墠
	// dispatcher-only 璺緞甯歌鐘舵€侊級銆?

	// First spawn 鍐欏叆銆?
	if _, err := store.RecordNodeSpawn(context.Background(), RecordNodeSpawnInput{
		DagKey: "dag-1", NodeKey: "n1", RunID: runID, ThreadID: "thread-1",
	}); err != nil {
		t.Fatalf("first spawn error = %v", err)
	}

	// 閲嶈瘯锛氳鐩栦細鍛戒腑缂哄け running run锛涗弗鏍?Fail-Fast 涓嬪繀椤昏繑鍥為敊璇紝
	// 涓嶈兘鎶?event append 澶辫触浼鎴愭垚鍔熴€?
	_, err := store.RecordNodeSpawn(context.Background(), RecordNodeSpawnInput{
		DagKey: "dag-1", NodeKey: "n1", RunID: runID, ThreadID: "thread-2",
	})
	if err == nil || !strings.Contains(err.Error(), "running run not found") {
		t.Fatalf("retry spawn (no running run) error = %v, want running run failure", err)
	}
}

func TestRecordNodeSpawn_RejectsCancelledRuntimeNode(t *testing.T) {
	store, db, now := newSpawnRecorderTestStore()
	runID := seedRunID(db, "dag-1", "run-A")
	seedRuntimeNode(t, db, now, runID, "n1", nil, "cancelled", "agent-a")

	_, err := store.RecordNodeSpawn(context.Background(), RecordNodeSpawnInput{
		DagKey: "dag-1", NodeKey: "n1", RunID: runID, ThreadID: "thread-late",
	})
	if err == nil || !errors.Is(err, platformdb.ErrNotFound) {
		t.Fatalf("RecordNodeSpawn cancelled node error = %v, want not found fence miss", err)
	}
	row := db.nodes[dagRunNodeKey("dag-1", "n1", runID)]
	if got := sqlc.TextPtr(row.SpawningThreadID); got != nil {
		t.Fatalf("cancelled node spawning_thread_id = %q, want nil", *got)
	}
}

func TestRecordNodeSpawn_RunScopedRowsAndEvents(t *testing.T) {
	store, db, now := newSpawnRecorderTestStore()
	runA := seedRunID(db, "dag-1", "run-a")
	runB := seedRunID(db, "dag-1", "run-b")
	seedRuntimeNode(t, db, now, runA, "n1", nil, "running", "agent-a")
	seedRuntimeNode(t, db, now, runB, "n1", nil, "running", "agent-a")

	mustRecordNodeSpawn(t, store, runA, "thread-a1", "run A first spawn")
	mustRecordNodeSpawn(t, store, runB, "thread-b1", "run B first spawn")
	res := mustRecordNodeSpawn(t, store, runA, "thread-a2", "run A retry spawn")
	if got := stringValue(res.Node.SpawningThreadID); got != "thread-a2" {
		t.Fatalf("run A returned spawning thread = %q, want thread-a2", got)
	}
	if res.RunKey != "run-a" {
		t.Fatalf("run A retry RunKey = %q, want run-a", res.RunKey)
	}
	assertRunScopedStoredThreads(t, db, runA, runB)
	assertRunScopedEvents(t, db)
}

func assertRunScopedStoredThreads(t *testing.T, db *fakeTaskDAGDB, runA, runB int64) {
	t.Helper()
	assertStoredSpawnThread(t, db.nodes[dagRunNodeKey("dag-1", "n1", runA)], "run A", "thread-a2")
	assertStoredSpawnThread(t, db.nodes[dagRunNodeKey("dag-1", "n1", runB)], "run B", "thread-b1")
}

func assertStoredSpawnThread(t *testing.T, node sqlc.TaskDagNode, label, want string) {
	t.Helper()
	got := sqlc.TextPtr(node.SpawningThreadID)
	if got == nil || *got != want {
		t.Fatalf("%s stored spawning thread = %v, want %s", label, got, want)
	}
}

func assertRunScopedEvents(t *testing.T, db *fakeTaskDAGDB) {
	t.Helper()
	var runAEvents []nodeSpawnEvent
	if err := json.Unmarshal(db.runs["run-a"].Events, &runAEvents); err != nil {
		t.Fatalf("decode run A events: %v", err)
	}
	if len(runAEvents) != 1 {
		t.Fatalf("run A events = %+v, want one a1->a2 event", runAEvents)
	}
	assertNodeSpawnEvent(t, runAEvents[0], "n1", "thread-a1", "thread-a2")
	if len(db.runs["run-b"].Events) != 0 {
		t.Fatalf("run B events = %s, want empty after only first spawn", db.runs["run-b"].Events)
	}
}

func assertNodeSpawnEvent(t *testing.T, event nodeSpawnEvent, wantNode, wantPrev, wantThread string) {
	t.Helper()
	if event.Kind != "node_spawn" || event.NodeKey != wantNode ||
		event.PrevThreadID != wantPrev || event.ThreadID != wantThread {
		t.Fatalf("event = %+v, want kind=node_spawn node_key=%s prev=%s new=%s", event, wantNode, wantPrev, wantThread)
	}
	if event.TS == "" {
		t.Fatalf("event ts empty, want RFC3339Nano timestamp")
	}
}

func TestRecordNodeSpawn_InputValidation(t *testing.T) {
	store, db, _ := newSpawnRecorderTestStore()
	seedDAG(t, db, db.now, []seedNode{
		{key: "n1", deps: nil, status: "running", agent: "agent-a"},
	})

	cases := []struct {
		name string
		in   RecordNodeSpawnInput
		want string // substring of expected error
	}{
		{"empty_thread_id", RecordNodeSpawnInput{DagKey: "dag-1", NodeKey: "n1"}, "thread_id required"},
		{"whitespace_thread_id", RecordNodeSpawnInput{DagKey: "dag-1", NodeKey: "n1", ThreadID: "   "}, "thread_id required"},
		{"empty_dag_key", RecordNodeSpawnInput{NodeKey: "n1", ThreadID: "t"}, "dag_key and node_key required"},
		{"empty_node_key", RecordNodeSpawnInput{DagKey: "dag-1", ThreadID: "t"}, "dag_key and node_key required"},
		{"missing_run_id", RecordNodeSpawnInput{DagKey: "dag-1", NodeKey: "n1", ThreadID: "t"}, "run_id required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.RecordNodeSpawn(context.Background(), tc.in)
			if err == nil {
				t.Fatalf("RecordNodeSpawn(%+v) error = nil, want %q", tc.in, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("RecordNodeSpawn error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func stringValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// TestRecordNodeSpawn_EventsRingTrim_KeepsLastFifty 楠岃瘉绔彛鏀舵暃 batch 瀹炶鐨?
// task_dag_runs.events 鐜舰鎴柇锛歛ppend 澶氫簬 50 鏉?node_spawn 浜嬩欢鏃讹紝浠呬繚鐣?
// 鏈€杩?50 鏉★紙鎸夋椂闂村簭鏈€鏃╃殑琚涪鍑猴級銆俁1 P1 #5 + R2 P0 #2 淇銆?
//
// 娴佺▼锛氳鍚屼竴鑺傜偣閲嶈瘯 N 娆★紙thread-1 鈫?thread-2 鈫?... 鈫?thread-N锛夛紝姣忔閲嶈瘯
// 锛坧rev != new锛夐兘浼?append 涓€鏉′簨浠躲€傛楠?N=60 鏃?events 闀垮害=50锛屼笖鏈€鏃╃殑
// 10 鏉″凡琚涪寮冿紝鏈€杩?50 鏉?thread_id 鏄?thread-11 鈥?thread-60 鑼冨洿銆?
//
// 50 闃堝€兼槸 AppendTaskDagRunEvent SQL 鍐?CASE 鍐欐鐨勶紱闃堝€煎彉鏇存椂鏀?SQL 鍚庢湰娴嬭瘯
// 璺熸敼 expectedCap 鍗冲彲銆?
func TestRecordNodeSpawn_EventsRingTrim_KeepsLastFifty(t *testing.T) {
	const expectedCap = 50
	const totalSpawns = 60

	store, db, now := newSpawnRecorderTestStore()
	runID := seedRunID(db, "dag-1", "run-A")
	seedRuntimeNode(t, db, now, runID, "n1", nil, "running", "agent-a")

	for i := 1; i <= totalSpawns; i++ {
		threadID := "thread-" + itoa(i)
		_, err := store.RecordNodeSpawn(context.Background(), RecordNodeSpawnInput{
			DagKey: "dag-1", NodeKey: "n1", RunID: runID, ThreadID: threadID,
		})
		if err != nil {
			t.Fatalf("spawn %d error = %v", i, err)
		}
	}

	run := db.runs["run-A"]
	var arr []map[string]any
	if err := json.Unmarshal(run.Events, &arr); err != nil {
		t.Fatalf("decode events: %v; raw=%s", err, run.Events)
	}
	// 60 spawns: 绗?1 娆￠鍙戜笉鍐?events锛坧rev 绌猴級锛岀 2..60 娆℃瘡娆″啓 1 鏉?鈫?鍏?59 鏉°€?
	// 59 > 50 鈫?鎴柇淇濈暀鏈€鍚?50 鏉°€傛渶鏃╀繚鐣欑殑鏄 11 娆″啓鍏ョ殑浜嬩欢锛堣鐩?thread-10鈫抰hread-11锛夈€?
	if len(arr) != expectedCap {
		t.Fatalf("events length = %d, want %d (ring trim)", len(arr), expectedCap)
	}
	// 绗竴鏉′繚鐣欑殑搴旀槸 thread_id=thread-11銆乸rev_thread_id=thread-10銆?
	first := arr[0]
	if first["thread_id"] != "thread-11" {
		t.Fatalf("first kept event thread_id = %v, want thread-11", first["thread_id"])
	}
	if first["prev_thread_id"] != "thread-10" {
		t.Fatalf("first kept event prev_thread_id = %v, want thread-10", first["prev_thread_id"])
	}
	// 鏈€鍚庝竴鏉″簲鏄渶鏂扮殑锛歵hread-60 / prev thread-59銆?
	last := arr[expectedCap-1]
	if last["thread_id"] != "thread-60" {
		t.Fatalf("last event thread_id = %v, want thread-60", last["thread_id"])
	}
	if last["prev_thread_id"] != "thread-59" {
		t.Fatalf("last event prev_thread_id = %v, want thread-59", last["prev_thread_id"])
	}
}

// itoa 鏈湴灏忚浆瀛楃涓诧紝閬垮厤寮?strconv锛堜繚鎸?import 琛ㄥ共鍑€锛夈€?
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
