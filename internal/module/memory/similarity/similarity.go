// Package similarity 负责"记忆中心相似度对"的整合与忽略持久化：
//   - 忽略 set 文件存储（.similarity-ignored.json，原子写 + 进程内互斥）；
//   - LLM 智能整合主流程（prompt 构造、调用 DreamExecutor、解析 decisions、应用到磁盘）。
//
// 与主包（internal/module/memory）的耦合通过 Deps 接口反向依赖：
// 主包提供 adapter 实现 Deps 来承接读盘 / 合并 / 调 LLM；子包自己定义最小数据类型，
// 不引用主包私有类型。这样把 ~480 行业务从主包搬出，保住 memory 主包文件数预算。
package similarity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/memory/dedup"
)

// ---------------------------------------------------------------------------
// Public types (子包独立的最小数据模型)
// ---------------------------------------------------------------------------

var ErrLLMConsolidate = errors.New("LLM consolidate")

// SimilarPair 是 UI 看到的一对相似条目，来自后端 buildUIMemorySnapshot。
type SimilarPair struct {
	NameA, NameB     string
	PathA, PathB     string
	TargetA, TargetB string
	Score            float64
}

// EntrySnapshot 是子包整合需要的最小 entry 信息（去掉主包 MemoryEntry 复杂字段）。
type EntrySnapshot struct {
	Name        string
	Description string
	Content     string
	Type        string // "feedback" / "project" / "user" / "reference"
}

// MergeRequest 描述一次"整合两条 entry"的写盘请求；MergedDescription/Content
// 非空时覆盖默认的 dedup.MergeContent 字面合并行为（LLM 整合走这条路径）。
type MergeRequest struct {
	CWD               string
	TargetA, PathA    string
	TargetB, PathB    string
	MergedDescription string
	MergedContent     string
}

// ConsolidateResult 是 ConsolidateAll 的返回统计。
//   - Merged: 实际写盘整合成功的对数；
//   - Ignored: LLM 判 merge=false 并被写入 ignored set 的对数；
//   - Failed: 应用决策时出错的对数（schema 错、写盘失败等）；
//   - Skipped: 输入相似对数但 LLM 没给出对应 decision 的对数；
//   - Errors: 失败 / 跳过对的原因明细（按对数限制为 ≤ 10 条避免回包过长）。
type ConsolidateResult struct {
	Merged  int
	Ignored int
	Failed  int
	Skipped int
	Errors  []string
}

// Deps 是子包与主包的反向依赖接口；主包提供 adapter 实现该接口。
// 子包通过 Deps 间接调用主包私有函数（read entry / merge / DreamExecutor），
// 主包不需要为子包导出大量私有 API。
type Deps interface {
	PrivateRoot(ctx context.Context, cwd string) (string, error)
	SimilarPairs(ctx context.Context, cwd string) ([]SimilarPair, error)
	ReadEntry(ctx context.Context, cwd, target, path string) (EntrySnapshot, error)
	Merge(ctx context.Context, req MergeRequest) error
	DreamExecute(ctx context.Context, prompt string) (string, error)
	Logger() *slog.Logger
}

// ---------------------------------------------------------------------------
// Ignored set 持久化（pairKey + load + append）
// ---------------------------------------------------------------------------

const ignoredFileName = ".similarity-ignored.json"

// ignoredFile 是 .similarity-ignored.json 的磁盘结构。
type ignoredFile struct {
	Pairs []string `json:"pairs"`
}

// IgnoreKey 生成一对相似条目的稳定 key（两侧顺序无关）。
func IgnoreKey(targetA, pathA, targetB, pathB string) string {
	a := strings.TrimSpace(targetA) + ":" + strings.TrimSpace(pathA)
	b := strings.TrimSpace(targetB) + ":" + strings.TrimSpace(pathB)
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

type ignoreMgr struct{ mu sync.Mutex }

var ignoreMgrInst = &ignoreMgr{}

// LoadIgnored 读取 privateRoot 下 .similarity-ignored.json 的 ignored set。
// privateRoot 为空、文件不存在或 JSON 解析失败都返回错误。
func LoadIgnored(privateRoot string) (map[string]struct{}, error) {
	root := strings.TrimSpace(privateRoot)
	if root == "" {
		return nil, errors.New("private root is empty")
	}
	path := filepath.Join(root, ignoredFileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("load similarity ignored: %w", err)
		}
		return nil, fmt.Errorf("load similarity ignored: %w", err)
	}
	if len(raw) == 0 {
		return map[string]struct{}{}, nil
	}
	var file ignoredFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse similarity ignored: %w", err)
	}
	set := make(map[string]struct{}, len(file.Pairs))
	for _, k := range file.Pairs {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		set[k] = struct{}{}
	}
	return set, nil
}

// AppendIgnored 把 key 追加到 ignored set 并原子写盘（已存在 → 幂等无操作）。
func AppendIgnored(privateRoot, key string) error {
	root := strings.TrimSpace(privateRoot)
	if root == "" {
		return errors.New("private root is empty")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("ignored key is empty")
	}
	ignoreMgrInst.mu.Lock()
	defer ignoreMgrInst.mu.Unlock()
	path := filepath.Join(root, ignoredFileName)
	if _, statErr := os.Stat(path); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return writeIgnored(root, map[string]struct{}{key: {}})
		}
		return fmt.Errorf("stat similarity ignored: %w", statErr)
	}
	set, err := LoadIgnored(root)
	if err != nil {
		return err
	}
	if _, exists := set[key]; exists {
		return nil
	}
	set[key] = struct{}{}
	return writeIgnored(root, set)
}

// writeIgnored 把 set 排序后原子写到 privateRoot/.similarity-ignored.json。
// 调用方必须持有 ignoreLock。
func writeIgnored(privateRoot string, set map[string]struct{}) error {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	raw, err := json.MarshalIndent(ignoredFile{Pairs: keys}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal similarity ignored: %w", err)
	}
	if err := os.MkdirAll(privateRoot, 0o755); err != nil {
		return fmt.Errorf("ensure private root: %w", err)
	}
	path := filepath.Join(privateRoot, ignoredFileName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return fmt.Errorf("write similarity ignored tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename similarity ignored: %w", err)
	}
	return nil
}

// IgnorePair 是"用户点忽略按钮"或"LLM 判 merge=false"的统一入口：
// 解析 privateRoot → 算 key → AppendIgnored。
func IgnorePair(ctx context.Context, deps Deps, cwd, targetA, pathA, targetB, pathB string) error {
	if strings.TrimSpace(pathA) == "" || strings.TrimSpace(pathB) == "" {
		return errors.New("pathA and pathB are required")
	}
	root, err := deps.PrivateRoot(ctx, cwd)
	if err != nil {
		return err
	}
	return AppendIgnored(root, IgnoreKey(targetA, pathA, targetB, pathB))
}

// ---------------------------------------------------------------------------
// LLM Prompt construction + decision parsing
// ---------------------------------------------------------------------------

// promptHeader 固化了 LLM 整合的指令语义。
const promptHeader = `你是 memory 整合助手。下面是 N 组语义相似的 memory 条目对，请逐组判断是否应当合并。

判定原则：
1. 应合（merge=true）：两条讲同一件规则 / 事实 / 偏好，去重对用户有益。
2. 不应合（merge=false）：字面相似但语义层级或主题不同（例：private 是个人偏好，team 是项目硬约定）。
3. 应合时保留侧（keep ∈ {"A","B"}，仅限大写字母）；scope 字段取值范围: {"private","team"}（不是 type）。
   a) 若两条 scope 字段值不同（一侧 "private"、一侧 "team"），保留 scope=="team" 那一侧 ——
      若 A.scope=="team" 则 keep="A"；若 B.scope=="team" 则 keep="B"。
      keep 字段值只能是 "A" 或 "B"，禁止输出 "team"/"private"/"first"/"a" 等其他值。
   b) 若同 scope，保留 description 信息密度更高（覆盖更多事实点，而非仅字数更长）的那条。
4. merge=true 时必须给出 merged_description 和 merged_content：
   - merged_description：≤ 200 个字符（中文按汉字数计，1 汉字 = 1 字符）；
   - merged_content：≤ 1500 个字符（同上单位）；
   - 二者必须使用与输入 a/b 相同的语言（通常中文）；
   - 去除冗余、保留两条共有 + 各自独有的关键信息；保持原语气与风格。
5. merge=false 时不需要 keep / merged_*；可选 reason 简述判定依据（≤ 80 个字符）。

输出约束：
- 严格 JSON，单一对象 {"decisions":[...]}，无 prose 前后缀，无 markdown fence；
- decisions 数量与输入 groups 数量一致，按 id 顺序排列、不重复 id；
- 字段名严格小写、值大小写如上规定。

输入：
`

// PairInput 是 LLM prompt 输入的一对条目（含 scope，用于 LLM 判定保留侧）。
type PairInput struct {
	ID   int            `json:"id"`
	Type string         `json:"type"`
	A    PairInputEntry `json:"a"`
	B    PairInputEntry `json:"b"`
}

type PairInputEntry struct {
	Scope       string `json:"scope"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"`
}

// AllInput 是 LLM prompt 输入 payload。
type AllInput struct {
	Groups []PairInput `json:"groups"`
}

// Decision 是 LLM 对单组的决策。
type Decision struct {
	ID                int    `json:"id"`
	Merge             bool   `json:"merge"`
	Keep              string `json:"keep,omitempty"`
	MergedDescription string `json:"merged_description,omitempty"`
	MergedContent     string `json:"merged_content,omitempty"`
	Reason            string `json:"reason,omitempty"`
}

// AllOutput 是 LLM 返回的解析后结构。
type AllOutput struct {
	Decisions []Decision `json:"decisions"`
}

// BuildPrompt 拼接 header + 序列化后的输入 JSON。单独抽出便于单测。
func BuildPrompt(input AllInput) (string, error) {
	body, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal consolidate input: %w", err)
	}
	return promptHeader + string(body), nil
}

// ParseDecisions 把 LLM 返回的 raw text 解析成 AllOutput。
// dreamexec.Run 已做 envelope unwrap + fence strip + balanced JSON 提取，raw 应直接是 JSON object。
func ParseDecisions(raw string) (AllOutput, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return AllOutput{}, errors.New("empty LLM response")
	}
	var out AllOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return AllOutput{}, fmt.Errorf("parse decisions JSON: %w", err)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// ConsolidateAll 主流程
// ---------------------------------------------------------------------------

// maxDescriptionRunes 是 LLM 整合产出的 description 软上限，与 prompt "≤ 200 字" 对齐。
const maxDescriptionRunes = 200

// maxErrorsInResult 限制 result.Errors 长度，避免单次回包巨大化。
const maxErrorsInResult = 10

// ConsolidateAll 是 RPC ui/memory/similarity/consolidate-all 的主流程：
//  1. 通过 deps.SimilarPairs 拿到当前相似对（已按 ignored 过滤）；
//  2. 没有对 → 零结果直接返回；
//  3. 调 deps.DreamExecute（含 ErrDreamExecutorNotConfigured 透传）；
//  4. 解析 decisions → applyDecisions 应用到磁盘；
//  5. 汇总返回 ConsolidateResult。
func ConsolidateAll(ctx context.Context, deps Deps, cwd string) (ConsolidateResult, error) {
	pairs, err := deps.SimilarPairs(ctx, cwd)
	if err != nil {
		return ConsolidateResult{}, err
	}
	if len(pairs) == 0 {
		return ConsolidateResult{}, nil
	}
	inputs, readErrors := loadPairInputs(ctx, deps, cwd, pairs)
	prompt, err := BuildPrompt(AllInput{Groups: inputs})
	if err != nil {
		return ConsolidateResult{}, err
	}
	raw, err := deps.DreamExecute(ctx, prompt)
	if err != nil {
		return ConsolidateResult{}, fmt.Errorf("%w: %w", ErrLLMConsolidate, err)
	}
	decisions, err := ParseDecisions(raw)
	if err != nil {
		return ConsolidateResult{}, err
	}
	return applyDecisions(ctx, deps, cwd, pairs, decisions.Decisions, readErrors), nil
}

// loadPairInputs 读取每对 entry 详情；读失败的对通过 readErrors 透传给 apply 阶段。
func loadPairInputs(ctx context.Context, deps Deps, cwd string, pairs []SimilarPair) ([]PairInput, map[int]error) {
	inputs := make([]PairInput, 0, len(pairs))
	readErrors := make(map[int]error)
	for idx, p := range pairs {
		entryA, err := deps.ReadEntry(ctx, cwd, p.TargetA, p.PathA)
		if err != nil {
			readErrors[idx] = fmt.Errorf("read keep side: %w", err)
			continue
		}
		entryB, err := deps.ReadEntry(ctx, cwd, p.TargetB, p.PathB)
		if err != nil {
			readErrors[idx] = fmt.Errorf("read absorb side: %w", err)
			continue
		}
		inputs = append(inputs, PairInput{
			ID:   idx,
			Type: entryA.Type,
			A:    PairInputEntry{Scope: p.TargetA, Name: entryA.Name, Description: entryA.Description, Content: entryA.Content},
			B:    PairInputEntry{Scope: p.TargetB, Name: entryB.Name, Description: entryB.Description, Content: entryB.Content},
		})
	}
	return inputs, readErrors
}

// applyDecisions 把 LLM 决策应用到磁盘。详细分类策略见 ConsolidateAll 文档。
//
// M5: 依赖污染场景注意 —— pairs 来自 dedup.FindSimilarPairs，可能出现同一 entry
// 跨多对（如 A↔B 与 A↔C）。本函数按顺序处理：pair[0] merge=true 删除 entry A 后，
// pair[1] 在 mergeUIMemoryEntries 内重读 A 会失败 → 计入 Failed（不损坏数据）。
// 用户看到 Failed 增加但 toast 文案不解释根因；当前接受该现象，未来可在
// loadPairInputs 阶段去重。
//
// M1: ctx 取消（用户切走页面）时主动短路，避免串行写盘继续浪费资源。
func applyDecisions(ctx context.Context, deps Deps, cwd string, pairs []SimilarPair, decisions []Decision, readErrors map[int]error) ConsolidateResult {
	byID := make(map[int]Decision, len(decisions))
	duplicateIDs := make(map[int]bool)
	for _, d := range decisions {
		if _, dup := byID[d.ID]; dup {
			duplicateIDs[d.ID] = true
			continue
		}
		byID[d.ID] = d
	}
	res := ConsolidateResult{}
	for idx, p := range pairs {
		if ctx.Err() != nil {
			// ctx canceled mid-loop: 停止后续 decision 应用，已成功的写入保留。
			break
		}
		applyOneDecision(ctx, deps, cwd, idx, p, byID, duplicateIDs, readErrors, &res)
	}
	return res
}

// applyOneDecision 把单对 (idx, pair) 的决策应用到磁盘，更新 res 累计字段。
// 分类策略（与 applyDecisions doc 对齐）：
//   - readErrors[idx]      → res.Failed (read entry failed)
//   - duplicateIDs[idx]    → res.Failed (LLM duplicate id)
//   - byID 缺 idx          → res.Skipped (no decision returned)
//   - d.Merge == false     → IgnorePair → res.Ignored / res.Failed
//   - d.Merge == true      → mergeWithDecision → res.Merged / res.Failed
func applyOneDecision(ctx context.Context, deps Deps, cwd string, idx int, p SimilarPair,
	byID map[int]Decision, duplicateIDs map[int]bool, readErrors map[int]error, res *ConsolidateResult) {
	if readErr, hasReadErr := readErrors[idx]; hasReadErr {
		res.Failed++
		logProblem(deps, "consolidate_read_failed", idx, p, readErr)
		res.Errors = appendBoundedError(res.Errors, fmt.Sprintf("group %d: read entry failed", idx))
		return
	}
	if duplicateIDs[idx] {
		res.Failed++
		msg := fmt.Errorf("LLM returned duplicate decision for id %d", idx)
		logProblem(deps, "consolidate_duplicate_id", idx, p, msg)
		res.Errors = appendBoundedError(res.Errors, fmt.Sprintf("group %d: %v", idx, msg))
		return
	}
	d, ok := byID[idx]
	if !ok {
		res.Skipped++
		logProblem(deps, "consolidate_missing_decision", idx, p, errors.New("no decision returned"))
		res.Errors = appendBoundedError(res.Errors, fmt.Sprintf("group %d: no decision returned", idx))
		return
	}
	if !d.Merge {
		if err := IgnorePair(ctx, deps, cwd, p.TargetA, p.PathA, p.TargetB, p.PathB); err != nil {
			res.Failed++
			logProblem(deps, "consolidate_ignore_failed", idx, p, err)
			res.Errors = appendBoundedError(res.Errors, fmt.Sprintf("group %d: ignore write failed", idx))
			return
		}
		res.Ignored++
		return
	}
	if err := mergeWithDecision(ctx, deps, cwd, p, d); err != nil {
		res.Failed++
		logProblem(deps, "consolidate_merge_failed", idx, p, err)
		res.Errors = appendBoundedError(res.Errors, fmt.Sprintf("group %d merge: %v", idx, err))
		return
	}
	res.Merged++
}

// logProblem 用 deps.Logger Warn 记录单 decision 失败原因；nil logger 安全。
func logProblem(deps Deps, op string, idx int, p SimilarPair, err error) {
	logger := deps.Logger()
	if logger == nil {
		return
	}
	logger.Warn("similarity consolidate problem",
		"op", op,
		"index", idx,
		"targetA", p.TargetA,
		"targetB", p.TargetB,
		"err", err.Error(),
	)
}

// mergeWithDecision 把 LLM decision 转成 MergeRequest 走 deps.Merge 写盘。
// 校验 keep 合法 + merged_* 非空 + content 长度兜底截断。
func mergeWithDecision(ctx context.Context, deps Deps, cwd string, p SimilarPair, d Decision) error {
	keep := strings.ToUpper(strings.TrimSpace(d.Keep))
	if keep != "A" && keep != "B" {
		return fmt.Errorf("invalid keep %q (must be A or B)", d.Keep)
	}
	mergedDesc := strings.TrimSpace(d.MergedDescription)
	mergedContent := strings.TrimSpace(d.MergedContent)
	if mergedDesc == "" || mergedContent == "" {
		return errors.New("merged_description and merged_content are required when merge=true")
	}
	// 截断方向：LLM merged_content 是单次重写产物，关键信息（总述/规则要点）
	// 通常在开头，因此按 rune 头部保留 + 尾部截断（不复用 dedup.TruncateOldestParagraphs
	// 那种"删开头保末段"的追加场景策略，避免砍掉总述段）。
	mergedContent = truncateRunesHead(mergedContent, dedup.MaxEntryContentRunes)
	mergedDesc = truncateRunesHead(mergedDesc, maxDescriptionRunes)

	var keepT, keepP, absorbT, absorbP string
	if keep == "A" {
		keepT, keepP = p.TargetA, p.PathA
		absorbT, absorbP = p.TargetB, p.PathB
	} else {
		keepT, keepP = p.TargetB, p.PathB
		absorbT, absorbP = p.TargetA, p.PathA
	}
	return deps.Merge(ctx, MergeRequest{
		CWD:               cwd,
		TargetA:           keepT,
		PathA:             keepP,
		TargetB:           absorbT,
		PathB:             absorbP,
		MergedDescription: mergedDesc,
		MergedContent:     mergedContent,
	})
}

// appendBoundedError 把 msg 追加到 list，但限制总数 ≤ maxErrorsInResult。
func appendBoundedError(list []string, msg string) []string {
	if len(list) >= maxErrorsInResult {
		return list
	}
	return append(list, msg)
}

// truncateRunesHead 按 rune 计数截断，避免中字节切断；超长加省略号。
// 子包自带实现，避免依赖主包 truncateRunes（属于私有 helper）。
func truncateRunesHead(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max == 1 {
		return string(runes[:1])
	}
	return string(runes[:max-1]) + "…"
}

// 保留 import 用途的 contract 引用（DreamExecute 错误识别）。
var _ = contract.ErrDreamExecutorNotConfigured
