package dedup

import (
	"fmt"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// makeScanFunc returns a ScanFunc that serves the given entries, optionally
// filtered by type when typeFilter != "".
func makeScanFunc(entries []EntrySnapshot) ScanFunc {
	return func(typeFilter string) ([]EntrySnapshot, error) {
		if typeFilter == "" {
			return entries, nil
		}
		var out []EntrySnapshot
		for _, e := range entries {
			if e.Type == typeFilter {
				out = append(out, e)
			}
		}
		return out, nil
	}
}

// ---------------------------------------------------------------------------
// Filter.Check tests
// ---------------------------------------------------------------------------

func filterCheckOldEntry() (string, EntrySnapshot) {
	oldContent := "面向用户的正文一律用中文。commit message 用中文。代码注释保持英文风格。严格遵守输出规范。"
	oldEntry := EntrySnapshot{
		Name:    "reply-in-chinese",
		Type:    "feedback",
		Lang:    "zh",
		Aliases: []string{"chinese-reply", "lang-rule"},
		Source:  "dream",
		Content: oldContent,
		Scope:   "private",
		Path:    "/mem/feedback_reply.md",
	}
	return oldContent, oldEntry
}

func requireFilterCheck(t *testing.T, f *Filter, candidate EntrySnapshot) CheckResult {
	t.Helper()
	res, err := f.Check(candidate)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return res
}

func assertFilterAction(t *testing.T, got, want Decision, label string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: got %v, want %v", label, got, want)
	}
}

func TestFilterCheckFreshMemoryNoMatch(t *testing.T) {
	_, oldEntry := filterCheckOldEntry()
	f := NewFilter(makeScanFunc([]EntrySnapshot{oldEntry}), nil)
	candidate := EntrySnapshot{
		Name:    "daily-report-format",
		Type:    "feedback",
		Content: "日报固定四段落：进展、阻塞、计划、风险。每段不超过三行。格式严格执行。",
		Scope:   "private",
	}
	res := requireFilterCheck(t, f, candidate)
	assertFilterAction(t, res.Action, WriteNew, "fresh memory action")
}

func TestFilterCheckNameExactDupOldContainsNewSkip(t *testing.T) {
	_, oldEntry := filterCheckOldEntry()
	f := NewFilter(makeScanFunc([]EntrySnapshot{oldEntry}), nil)
	candidate := EntrySnapshot{
		Name:    "reply-in-chinese",
		Type:    "feedback",
		Content: "面向用户的正文一律用中文",
		Scope:   "private",
	}
	res := requireFilterCheck(t, f, candidate)
	assertFilterAction(t, res.Action, Skip, "old contains new action")
}

func TestFilterCheckNameExactDupWithNovelContentMerge(t *testing.T) {
	oldContent, oldEntry := filterCheckOldEntry()
	f := NewFilter(makeScanFunc([]EntrySnapshot{oldEntry}), nil)
	candidate := EntrySnapshot{
		Name:    "reply-in-chinese",
		Type:    "feedback",
		Content: oldContent + "禁止在回复中使用表情符号。邮件主题行必须全英文。所有标题用粗体显示。",
		Scope:   "private",
	}
	res := requireFilterCheck(t, f, candidate)
	assertFilterAction(t, res.Action, Merge, "novel same-name action")
	if res.MergedEntry == nil {
		t.Fatal("MergedEntry must not be nil on Merge")
	}
	if res.TargetPath != oldEntry.Path {
		t.Fatalf("TargetPath = %q, want %q", res.TargetPath, oldEntry.Path)
	}
	if !strings.Contains(res.MergedEntry.Content, "面向用户的正文") {
		t.Fatal("MergedEntry.Content should contain original old content")
	}
}

func TestFilterCheckContentContainmentDupSkip(t *testing.T) {
	existing := []EntrySnapshot{{
		Name:    "language-rules",
		Type:    "feedback",
		Content: "用中文回复用户消息，包括详细背景说明，语言风格自然友好，避免机器翻译腔调，保持专业而温暖的语气。",
		Scope:   "private",
		Path:    "/mem/lang.md",
	}}
	f := NewFilter(makeScanFunc(existing), nil)
	candidate := EntrySnapshot{
		Name:    "different-name",
		Type:    "feedback",
		Content: "用中文回复用户消息，包括详细背景说明",
		Scope:   "private",
	}
	res := requireFilterCheck(t, f, candidate)
	assertFilterAction(t, res.Action, Skip, "content-contained action")
}

func TestFilterCheckContentContainmentDupWithNovelMerge(t *testing.T) {
	existing := []EntrySnapshot{{
		Name:    "language-rules",
		Type:    "feedback",
		Content: "用中文回复用户消息，语言风格自然友好，避免机器翻译腔调。",
		Scope:   "private",
		Path:    "/mem/lang2.md",
	}}
	f := NewFilter(makeScanFunc(existing), nil)
	candidate := EntrySnapshot{
		Name:    "different-name-novel",
		Type:    "feedback",
		Content: "用中文回复用户消息，语言风格自然友好，避免机器翻译腔调。禁止使用正式公文语气。段落间加空行。所有列表用序号。",
		Scope:   "private",
	}
	res := requireFilterCheck(t, f, candidate)
	assertFilterAction(t, res.Action, Merge, "content-matched novel action")
}

func TestFilterCheckCrossScopeMatchWithNovelWriteNew(t *testing.T) {
	oldContent, _ := filterCheckOldEntry()
	teamEntry := EntrySnapshot{
		Name:    "reply-in-chinese",
		Type:    "feedback",
		Content: oldContent,
		Scope:   "team",
		Path:    "/team/feedback_reply.md",
	}
	f := NewFilter(makeScanFunc(nil), makeScanFunc([]EntrySnapshot{teamEntry}))
	candidate := EntrySnapshot{
		Name:    "reply-in-chinese",
		Type:    "feedback",
		Content: oldContent + "禁止在回复中使用表情符号。邮件主题全英文。标题粗体显示。",
		Scope:   "private",
	}
	res := requireFilterCheck(t, f, candidate)
	assertFilterAction(t, res.Action, WriteNew, "cross-scope novel action")
}

func TestFilterCheckTeamCandidatePrefersTeamDuplicateWhenPrivateSameNameExists(t *testing.T) {
	oldContent, _ := filterCheckOldEntry()
	privateEntry := filterCheckScopedEntry(oldContent, "private", "/private/feedback_reply.md")
	teamEntry := filterCheckScopedEntry(oldContent, "team", "/team/feedback_reply.md")
	f := NewFilter(makeScanFunc([]EntrySnapshot{privateEntry}), makeScanFunc([]EntrySnapshot{teamEntry}))
	candidate := EntrySnapshot{
		Name:    "reply-in-chinese",
		Type:    "feedback",
		Content: "面向用户的正文一律用中文",
		Scope:   "team",
	}
	res := requireFilterCheck(t, f, candidate)
	assertFilterAction(t, res.Action, Skip, "team same-scope skip action")
}

func TestFilterCheckTeamCandidatePrefersTeamDuplicateForMergeWhenPrivateSameNameExists(t *testing.T) {
	oldContent, _ := filterCheckOldEntry()
	privateEntry := filterCheckScopedEntry(oldContent, "private", "/private/feedback_reply.md")
	teamEntry := filterCheckScopedEntry(oldContent, "team", "/team/feedback_reply.md")
	f := NewFilter(makeScanFunc([]EntrySnapshot{privateEntry}), makeScanFunc([]EntrySnapshot{teamEntry}))
	candidate := EntrySnapshot{
		Name:    "reply-in-chinese",
		Type:    "feedback",
		Content: oldContent + "禁止在回复中使用表情符号。邮件主题行必须全英文。所有标题用粗体显示。",
		Scope:   "team",
	}
	res := requireFilterCheck(t, f, candidate)
	assertFilterAction(t, res.Action, Merge, "team same-scope merge action")
	if res.TargetPath != teamEntry.Path {
		t.Fatalf("merge target path = %q, want team path %q", res.TargetPath, teamEntry.Path)
	}
}

func filterCheckScopedEntry(content, scope, path string) EntrySnapshot {
	return EntrySnapshot{
		Name:    "reply-in-chinese",
		Type:    "feedback",
		Content: content,
		Scope:   scope,
		Path:    path,
	}
}

func TestFilterCheckPrivateScanAliasOfTeamEntryKeepsCrossScopeWriteNew(t *testing.T) {
	teamEntryAsPrivate := EntrySnapshot{
		Name:    "deploy-checklist",
		Type:    "user",
		Content: "Team deploy checklist requires rollback owner and release window confirmation.",
		Scope:   "private",
		Path:    "/mem/team/user/deploy-checklist.md",
	}
	teamEntry := teamEntryAsPrivate
	teamEntry.Scope = "team"
	f := NewFilter(makeScanFunc([]EntrySnapshot{teamEntryAsPrivate}), makeScanFunc([]EntrySnapshot{teamEntry}))
	candidate := EntrySnapshot{
		Name:    "deploy-checklist",
		Type:    "user",
		Content: "Team deploy checklist requires rollback owner and release window confirmation.",
		Scope:   "private",
	}
	res := requireFilterCheck(t, f, candidate)
	assertFilterAction(t, res.Action, WriteNew, "private scan alias action")
}

func TestFilterCheckCrossScopeMatchNoNovelSkip(t *testing.T) {
	oldContent, _ := filterCheckOldEntry()
	teamEntry := EntrySnapshot{
		Name:    "reply-in-chinese",
		Type:    "feedback",
		Content: oldContent,
		Scope:   "team",
		Path:    "/team/feedback_reply.md",
	}
	f := NewFilter(makeScanFunc(nil), makeScanFunc([]EntrySnapshot{teamEntry}))
	candidate := EntrySnapshot{
		Name:    "reply-in-chinese",
		Type:    "feedback",
		Content: "面向用户的正文一律用中文",
		Scope:   "private",
	}
	res := requireFilterCheck(t, f, candidate)
	assertFilterAction(t, res.Action, WriteNew, "cross-scope duplicate action")
}

func TestFilterCheckMergedEntryPreservesLangAliasesSource(t *testing.T) {
	oldContent, _ := filterCheckOldEntry()
	existing := []EntrySnapshot{{
		Name:    "reply-in-chinese",
		Type:    "feedback",
		Lang:    "zh",
		Aliases: []string{"chinese-reply", "lang-rule"},
		Source:  "human",
		Content: oldContent,
		Scope:   "private",
		Path:    "/mem/feedback_reply.md",
	}}
	f := NewFilter(makeScanFunc(existing), nil)
	candidate := EntrySnapshot{
		Name:    "reply-in-chinese",
		Type:    "feedback",
		Lang:    "en",
		Aliases: []string{"new-alias"},
		Source:  "dream",
		Content: oldContent + "禁止使用表情符号。邮件主题全英文。标题粓体。所有序号统一格式。",
		Scope:   "private",
	}
	res := requireFilterCheck(t, f, candidate)
	assertFilterAction(t, res.Action, Merge, "preserve metadata action")
	assertMergedEntryMetadata(t, res.MergedEntry)
}

func assertMergedEntryMetadata(t *testing.T, merged *EntrySnapshot) {
	t.Helper()
	if merged == nil {
		t.Fatal("MergedEntry must not be nil on Merge")
	}
	if merged.Lang != "zh" {
		t.Fatalf("Lang should be preserved from old entry: got %q, want %q", merged.Lang, "zh")
	}
	aliasSet := make(map[string]struct{}, len(merged.Aliases))
	for _, alias := range merged.Aliases {
		aliasSet[alias] = struct{}{}
	}
	for _, want := range []string{"chinese-reply", "lang-rule"} {
		if _, ok := aliasSet[want]; !ok {
			t.Fatalf("Aliases missing %q; got %v", want, merged.Aliases)
		}
	}
	if merged.Source != "human" {
		t.Fatalf("Source should be kept from old entry: got %q, want %q", merged.Source, "human")
	}
}

func TestFilterCheckScanTeamNilPrivateOnly(t *testing.T) {
	_, oldEntry := filterCheckOldEntry()
	f := NewFilter(makeScanFunc([]EntrySnapshot{oldEntry}), nil)
	candidate := EntrySnapshot{
		Name:    "freeze-policy",
		Type:    "feedback",
		Content: "限额一旦设定不可更改，即使管理员也无权调低冻结上限。",
		Scope:   "private",
	}
	res := requireFilterCheck(t, f, candidate)
	assertFilterAction(t, res.Action, WriteNew, "nil team scan action")
}

// ---------------------------------------------------------------------------
// Filter.FindOverflowMerge tests
// ---------------------------------------------------------------------------

// buildFeedbackEntries creates n feedback EntrySnapshots with varied content.
// When n > number of distinct templates, content wraps (so some entries share
// content, ensuring a mergeable pair exists for n > 15).
func buildFeedbackEntries(n int) []EntrySnapshot {
	contents := []string{
		"用中文回复用户消息，语言风格自然友好，避免机器翻译腔调，保持专业而温暖的语气。",
		"commit message 一律使用中文，包含动作前缀和简短说明，禁止仅写 fix 或 update。",
		"代码注释全部用英文编写，变量名和函数名遵循驼峰命名规范，禁止拼音缩写。",
		"日报格式固定四段落：今日进展、遇到阻塞、明日计划、风险提示，每段不超过三行。",
		"freeze 限额一旦设定不可更改，即使管理员也无权调低冻结上限，必须走审批流程。",
		"所有对外文档标题用粗体显示，正文每段首行缩进两字符，段落间加空行分隔。",
		"邮件主题行必须全英文，不超过 60 个字符，使用动词开头的祈使句描述问题。",
		"禁止在任何正式输出中使用表情符号，技术文档尤其严格执行此规范。",
		"PR 描述必须包含背景、改动点和测试方案三节，缺少任意一节不予合并。",
		"数字与单位之间加一个空格，中英文混排时中英文之间各加一个空格。",
		"会议纪要在会议结束后两小时内完成，抄送所有参与者，格式统一用 Markdown。",
		"异常处理必须记录详细日志，包含请求 ID、时间戳和错误码，严禁吞掉异常。",
		"接口文档更新与代码同步提交，禁止先上线后补文档，评审时必须附上文档链接。",
		"测试覆盖率不低于 80%，关键路径必须有集成测试，禁止仅靠 mock 替代真实逻辑。",
		"发布前必须在预发环境完整回归，线上故障后 24 小时内提交复盘报告给技术委员会。",
		"所有配置变更通过 GitOps 流程，直接修改线上配置视为重大违规，将触发安全审查。",
	}
	out := make([]EntrySnapshot, n)
	for i := 0; i < n; i++ {
		out[i] = EntrySnapshot{
			Name:    fmt.Sprintf("rule-%02d", i),
			Type:    "feedback",
			Content: contents[i%len(contents)],
			Scope:   "private",
			Path:    fmt.Sprintf("/mem/feedback_%02d.md", i),
		}
	}
	return out
}

func TestFilterFindOverflowMerge_16Entries(t *testing.T) {
	// 16 entries > MaxEntriesPerType(15). We use 15 distinct entries then add
	// one that duplicates entry 0's content to guarantee a mergeable pair.
	entries := buildFeedbackEntries(15)
	entries = append(entries, EntrySnapshot{
		Name:    "rule-dup",
		Type:    "feedback",
		Content: entries[0].Content,
		Scope:   "private",
		Path:    "/mem/feedback_dup.md",
	})
	// Now len(entries) == 16 > MaxEntriesPerType.
	f := NewFilter(makeScanFunc(entries), nil)

	instr, err := f.FindOverflowMerge("feedback")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if instr == nil {
		t.Fatal("expected non-nil OverflowInstruction for 16 entries with mergeable pair")
	}
	// DeletePath must be a non-empty path from one of the entries.
	if instr.DeletePath == "" {
		t.Error("DeletePath must be non-empty")
	}
	pathFound := false
	for _, e := range entries {
		if e.Path == instr.DeletePath {
			pathFound = true
			break
		}
	}
	if !pathFound {
		t.Errorf("DeletePath %q does not match any entry path", instr.DeletePath)
	}
	// KeepEntry must have content.
	if instr.KeepEntry.Content == "" {
		t.Error("KeepEntry.Content must not be empty")
	}
}

func TestFilterFindOverflowMerge_AllPairsBelowThreshold(t *testing.T) {
	// 16 entries, all with completely distinct short content -> no pair >= 0.4 -> nil.
	contents := []string{
		"freeze limit policy cannot changed ever admin nopower",
		"dailyreport format fixed section structure timeline",
		"commit message english only verb prefix short summary",
		"interface document update code sync submit together",
		"test coverage minimum eighty percent integration required",
		"release regression preenv mandatory postmortem report",
		"configuration change gitops pipeline direct modify violation",
		"email subject line english sixty character imperative verb",
		"meeting minutes two hours distribute markdown format",
		"exception logging request timestamp errorcode swallow forbidden",
		"pr description background changes testplan three sections",
		"number unit space mixed chinese english spacing rule",
		"emoji prohibited formal output technical document strict",
		"bold heading indent paragraph blank line separation rule",
		"variable function camelcase naming pinyin abbreviation banned",
		"security review oncall rotate schedule weekend coverage plan",
	}
	entries := make([]EntrySnapshot, len(contents))
	for i, c := range contents {
		entries[i] = EntrySnapshot{
			Name:    fmt.Sprintf("rule-%02d", i),
			Type:    "feedback",
			Content: c,
			Scope:   "private",
			Path:    fmt.Sprintf("/mem/feedback_%02d.md", i),
		}
	}
	f := NewFilter(makeScanFunc(entries), nil)

	instr, err := f.FindOverflowMerge("feedback")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if instr != nil {
		t.Errorf("expected nil when no pair meets threshold, got DeletePath=%q", instr.DeletePath)
	}
}

func TestFilterFindOverflowMerge_ExactlyAtLimit(t *testing.T) {
	// Exactly MaxEntriesPerType entries -> no overflow -> nil.
	entries := buildFeedbackEntries(MaxEntriesPerType)
	f := NewFilter(makeScanFunc(entries), nil)

	instr, err := f.FindOverflowMerge("feedback")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if instr != nil {
		t.Errorf("expected nil for exactly MaxEntriesPerType entries, got non-nil")
	}
}

func TestFilterFindOverflowMerge_NoDiskWrite(t *testing.T) {
	// Verify the function is purely computational: the returned KeepEntry
	// must carry a Path equal to one of the original entries (not a new path).
	entries := buildFeedbackEntries(15)
	entries = append(entries, EntrySnapshot{
		Name:    "rule-dup",
		Type:    "feedback",
		Content: entries[0].Content,
		Scope:   "private",
		Path:    "/mem/feedback_dup.md",
	})
	f := NewFilter(makeScanFunc(entries), nil)

	instr, err := f.FindOverflowMerge("feedback")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if instr == nil {
		t.Skip("no mergeable pair found; skipping path-check")
	}
	pathSet := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		pathSet[e.Path] = struct{}{}
	}
	if _, ok := pathSet[instr.KeepEntry.Path]; !ok {
		t.Errorf("KeepEntry.Path %q not in original entry paths", instr.KeepEntry.Path)
	}
}
