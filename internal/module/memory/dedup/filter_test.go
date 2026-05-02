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

func TestFilterCheck(t *testing.T) {
	// Shared "old" entry used across multiple sub-tests.
	// Content is long enough to produce many meaningful bigrams.
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

	t.Run("fresh_memory_no_match", func(t *testing.T) {
		existing := []EntrySnapshot{oldEntry}
		f := NewFilter(makeScanFunc(existing), nil)

		candidate := EntrySnapshot{
			Name:    "daily-report-format",
			Type:    "feedback",
			Content: "日报固定四段落：进展、阻塞、计划、风险。每段不超过三行。格式严格执行。",
			Scope:   "private",
		}
		res, err := f.Check(candidate)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Action != WriteNew {
			t.Errorf("expected WriteNew, got %v", res.Action)
		}
	})

	t.Run("name_exact_dup_old_contains_new_skip", func(t *testing.T) {
		// Candidate content is a strict subset of the old content.
		// Nearly all candidate bigrams exist in the old entry -> Skip.
		existing := []EntrySnapshot{oldEntry}
		f := NewFilter(makeScanFunc(existing), nil)

		// Use the first part of oldContent -- largely contained in old.
		subContent := "面向用户的正文一律用中文"
		candidate := EntrySnapshot{
			Name:    "reply-in-chinese", // exact name match
			Type:    "feedback",
			Content: subContent,
			Scope:   "private",
		}
		res, err := f.Check(candidate)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Action != Skip {
			t.Errorf("expected Skip (old contains new >=90%%), got %v", res.Action)
		}
	})

	t.Run("name_exact_dup_with_novel_content_merge", func(t *testing.T) {
		// Candidate has same name but adds genuinely new rules (>=15% novel bigrams).
		existing := []EntrySnapshot{oldEntry}
		f := NewFilter(makeScanFunc(existing), nil)

		// Append brand-new sentences that share almost no bigrams with oldContent.
		newContent := oldContent + "禁止在回复中使用表情符号。邮件主题行必须全英文。所有标题用粗体显示。"
		candidate := EntrySnapshot{
			Name:    "reply-in-chinese",
			Type:    "feedback",
			Content: newContent,
			Scope:   "private",
		}
		res, err := f.Check(candidate)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Action != Merge {
			t.Errorf("expected Merge, got %v", res.Action)
		}
		if res.MergedEntry == nil {
			t.Fatal("MergedEntry must not be nil on Merge")
		}
		if res.TargetPath != oldEntry.Path {
			t.Errorf("TargetPath = %q, want %q", res.TargetPath, oldEntry.Path)
		}
		// Merged content must contain the original old text.
		if !strings.Contains(res.MergedEntry.Content, "面向用户的正文") {
			t.Error("MergedEntry.Content should contain original old content")
		}
	})

	t.Run("content_containment_dup_skip", func(t *testing.T) {
		// No name match, but candidate content is fully contained inside existing.
		existing := []EntrySnapshot{{
			Name:    "language-rules",
			Type:    "feedback",
			Content: "用中文回复用户消息，包括详细背景说明，语言风格自然友好，避免机器翻译腔调，保持专业而温暖的语气。",
			Scope:   "private",
			Path:    "/mem/lang.md",
		}}
		f := NewFilter(makeScanFunc(existing), nil)

		// Candidate: short subset of the existing content.
		candidate := EntrySnapshot{
			Name:    "different-name",
			Type:    "feedback",
			Content: "用中文回复用户消息，包括详细背景说明",
			Scope:   "private",
		}
		res, err := f.Check(candidate)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Action != Skip {
			t.Errorf("expected Skip for content-contained candidate, got %v", res.Action)
		}

	})

	t.Run("content_containment_dup_with_novel_merge", func(t *testing.T) {
		// Candidate matches by content containment and has novel content -> Merge.
		existing := []EntrySnapshot{{
			Name:    "language-rules",
			Type:    "feedback",
			Content: "用中文回复用户消息，语言风格自然友好，避免机器翻译腔调。",
			Scope:   "private",
			Path:    "/mem/lang2.md",
		}}
		f := NewFilter(makeScanFunc(existing), nil)

		// Candidate: overlapping content plus significant new addition.
		candidate := EntrySnapshot{
			Name:    "different-name-novel",
			Type:    "feedback",
			Content: "用中文回复用户消息，语言风格自然友好，避免机器翻译腔调。禁止使用正式公文语气。段落间加空行。所有列表用序号。",
			Scope:   "private",
		}
		res, err := f.Check(candidate)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Action != Merge {
			t.Errorf("expected Merge for content-matched candidate with novel content, got %v", res.Action)
		}

	})

	t.Run("cross_scope_match_with_novel_write_new", func(t *testing.T) {
		// Existing entry is in "team" scope; candidate is in "private" scope.
		// Even with novel content the filter must NOT merge across scopes -> WriteNew.
		teamEntry := EntrySnapshot{
			Name:    "reply-in-chinese",
			Type:    "feedback",
			Content: oldContent,
			Scope:   "team",
			Path:    "/team/feedback_reply.md",
		}
		// scanPrivate returns nothing; scanTeam returns teamEntry.
		f := NewFilter(
			makeScanFunc(nil),
			makeScanFunc([]EntrySnapshot{teamEntry}),
		)

		newContent2 := oldContent + "禁止在回复中使用表情符号。邮件主题全英文。标题粗体显示。"
		candidate := EntrySnapshot{
			Name:    "reply-in-chinese",
			Type:    "feedback",
			Content: newContent2,
			Scope:   "private",
		}
		res, err := f.Check(candidate)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Action != WriteNew {
			t.Errorf("cross-scope merge must not happen: expected WriteNew, got %v", res.Action)
		}
	})

	t.Run("cross_scope_match_no_novel_skip", func(t *testing.T) {
		// Existing in "team"; candidate in "private" with content contained in team entry.
		// Cross-scope duplicates must not skip the current scope write.
		teamEntry := EntrySnapshot{
			Name:    "reply-in-chinese",
			Type:    "feedback",
			Content: oldContent,
			Scope:   "team",
			Path:    "/team/feedback_reply.md",
		}
		f := NewFilter(
			makeScanFunc(nil),
			makeScanFunc([]EntrySnapshot{teamEntry}),
		)

		// Candidate is the same short subset -- fully contained in the team entry.
		candidate := EntrySnapshot{
			Name:    "reply-in-chinese",
			Type:    "feedback",
			Content: "面向用户的正文一律用中文",
			Scope:   "private",
		}
		res, err := f.Check(candidate)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Action != WriteNew {
			t.Errorf("cross-scope duplicate must still write current scope: expected WriteNew, got %v", res.Action)
		}
	})

	t.Run("merged_entry_preserves_lang_aliases_source", func(t *testing.T) {
		// Verify that Lang, Aliases, and Source are not lost after a Merge.
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

		newContent3 := oldContent + "禁止使用表情符号。邮件主题全英文。标题粓体。所有序号统一格式。"
		candidate := EntrySnapshot{
			Name:    "reply-in-chinese",
			Type:    "feedback",
			Lang:    "en",                  // should be overridden by old
			Aliases: []string{"new-alias"}, // old's aliases should be kept
			Source:  "dream",               // old is "human" -> keep "human"
			Content: newContent3,
			Scope:   "private",
		}
		res, err := f.Check(candidate)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Action != Merge {
			t.Fatalf("expected Merge, got %v", res.Action)
		}
		m := res.MergedEntry
		if m.Lang != "zh" {
			t.Errorf("Lang should be preserved from old entry: got %q, want %q", m.Lang, "zh")
		}
		// Aliases: old aliases must be present in merged result.
		aliasSet := make(map[string]struct{}, len(m.Aliases))
		for _, a := range m.Aliases {
			aliasSet[a] = struct{}{}
		}
		for _, want := range []string{"chinese-reply", "lang-rule"} {
			if _, ok := aliasSet[want]; !ok {
				t.Errorf("Aliases missing %q; got %v", want, m.Aliases)
			}
		}
		// Source: old is "human" (not "dream") -> keep "human".
		if m.Source != "human" {
			t.Errorf("Source should be kept from old entry: got %q, want %q", m.Source, "human")
		}
	})

	t.Run("scan_team_nil_private_only", func(t *testing.T) {
		// scanTeam is nil -- filter must still work using private entries only.
		existing := []EntrySnapshot{oldEntry}
		f := NewFilter(makeScanFunc(existing), nil)

		// A totally new candidate.
		candidate := EntrySnapshot{
			Name:    "freeze-policy",
			Type:    "feedback",
			Content: "限额一旦设定不可更改，即使管理员也无权调低冻结上限。",
			Scope:   "private",
		}
		res, err := f.Check(candidate)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.Action != WriteNew {
			t.Errorf("expected WriteNew with nil scanTeam, got %v", res.Action)
		}
	})
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
