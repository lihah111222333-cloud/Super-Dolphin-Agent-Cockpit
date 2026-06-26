package dbquery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

const maxQueryRows = 10000
const queryOnlyCleanupTimeout = 5 * time.Second

var (
	allowedTables = map[string]struct{}{
		"agent_interactions":     {},
		"agent_provider_binding": {},
		"agent_status":           {},
		"agent_threads":          {},
		"audit_events":           {},
		"bus_exception_logs":     {},
		"command_card_runs":      {},
		"command_card_versions":  {},
		"command_cards":          {},
		"cwd_instance_locks":     {},
		"prompt_templates":       {},
		"prompt_versions":        {},
		"shared_files":           {},
		"system_logs":            {},
		"task_dag_nodes":         {},
		"task_dag_runs":          {},
		"task_dags":              {},
		"topology_approvals":     {},
		"ui_preferences":         {},
		"workspace_run_files":    {},
		"workspace_runs":         {},
	}
	dangerousKeywordPattern      = regexp.MustCompile(`(?i)\b(insert|update|delete|drop|alter|truncate|create|grant|revoke|comment|vacuum|analyze|copy|merge|call|do|attach|detach|pragma|reindex|replace|returning)\b`)
	dangerousFunctionCallPattern = regexp.MustCompile(`(?i)\b(load_extension|writefile|current_user|last_insert_rowid|changes|total_changes|pg_sleep|pg_terminate_backend|pg_cancel_backend|set_config|version|current_setting|inet_server_addr|inet_server_port|pg_read_file|pg_read_binary_file|pg_ls_dir|pg_stat_\w+|lo_import|lo_export)\b\s*\(`)
	dangerousBareFunctionPattern = regexp.MustCompile(`(?i)\bcurrent_user\b`)
	placeholderPattern           = regexp.MustCompile(`\$(\d+)`)
	sqlitePlaceholderPattern     = regexp.MustCompile(`\?`)
	errInvalidCTESyntax          = errors.New("dbquery query has invalid CTE syntax")
)

// executeQuery 执行经过校验的只读 SQL 并返回字段名映射结果。
// 查询始终走 SQLite query_only 独占连接，并在读取结束后通过 finish 恢复连接状态。
func executeQuery(ctx context.Context, queryer platformdb.Queryable, timeout time.Duration, query string, args ...any) (_ []map[string]any, err error) {
	ctx, err = prepareQueryContext(ctx, queryer, query, len(args))
	if err != nil {
		return nil, err
	}
	queryCtx, cancel := withQueryTimeout(ctx, timeout)
	defer cancel()
	rows, finish, err := openSQLiteReadOnlyRows(queryCtx, queryer, query, normalizeArgs(args)...)
	if err != nil {
		return nil, err
	}
	defer finalizeQuery(&err, finish)
	fields, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0)
	for rows.Next() {
		if len(result) >= maxQueryRows {
			return nil, fmt.Errorf("dbquery query exceeded row limit %d", maxQueryRows)
		}
		scanDest := make([]any, len(fields))
		scanPtrs := make([]any, len(fields))
		for i := range scanDest {
			scanPtrs[i] = &scanDest[i]
		}
		if err := rows.Scan(scanPtrs...); err != nil {
			return nil, err
		}
		result = append(result, rowValues(fields, scanDest))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// sqliteReadOnlyConnector 表示能为 dbquery 提供独占 SQLite 连接的查询源。
// dbquery 需要在连接级别打开 query_only，因此不能复用普通 QueryContext 接口。
type sqliteReadOnlyConnector interface {
	Conn(context.Context) (*sql.Conn, error)
}

// openSQLiteReadOnlyRows 在独占连接上开启 query_only 并执行只读 SQL。
// 返回的 finish 必须由调用方在 rows 读取结束后执行，用来关闭 rows、结束事务并恢复连接状态。
func openSQLiteReadOnlyRows(ctx context.Context, queryer platformdb.Queryable, query string, args ...any) (*sql.Rows, platformdb.QueryFinish, error) {
	connector, ok := queryer.(sqliteReadOnlyConnector)
	if !ok {
		return nil, nil, fmt.Errorf("dbquery requires a dedicated SQLite connection provider, got %T", queryer)
	}
	conn, err := connector.Conn(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("open dbquery dedicated SQLite connection: %w", err)
	}
	cleanup := func(success bool, tx *sql.Tx, rows *sql.Rows, queryErr error) error {
		return cleanupSQLiteReadOnlyQuery(ctx, conn, tx, rows, success, queryErr)
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA query_only = ON"); err != nil {
		return nil, nil, cleanup(false, nil, nil, fmt.Errorf("enable SQLite query_only: %w", err))
	}
	var queryOnly int
	if err := conn.QueryRowContext(ctx, "PRAGMA query_only").Scan(&queryOnly); err != nil {
		return nil, nil, cleanup(false, nil, nil, fmt.Errorf("verify SQLite query_only: %w", err))
	}
	if queryOnly != 1 {
		return nil, nil, cleanup(false, nil, nil, fmt.Errorf("verify SQLite query_only: got %d, want 1", queryOnly))
	}
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, cleanup(false, nil, nil, fmt.Errorf("begin dbquery read-only tx: %w", err))
	}
	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return nil, nil, cleanup(false, tx, nil, fmt.Errorf("parse dbquery SQL: %w", err))
	}
	if err := stmt.Close(); err != nil {
		return nil, nil, cleanup(false, tx, nil, fmt.Errorf("close parsed dbquery SQL: %w", err))
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, cleanup(false, tx, nil, err)
	}
	return rows, func(success bool) error {
		return cleanup(success, tx, rows, nil)
	}, nil
}

// cleanupSQLiteReadOnlyQuery 按 rows、事务、连接的顺序释放只读查询资源。
// 即使原始查询失败，也会尝试关闭 query_only 并归还连接，最后合并原始错误和清理错误。
func cleanupSQLiteReadOnlyQuery(ctx context.Context, conn *sql.Conn, tx *sql.Tx, rows *sql.Rows, success bool, queryErr error) error {
	var cleanupErr error
	if rows != nil {
		cleanupErr = errors.Join(cleanupErr, rows.Close())
	}
	if tx != nil {
		if success && queryErr == nil {
			cleanupErr = errors.Join(cleanupErr, tx.Commit())
		} else if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	cleanupCtx, cancel := platformconfig.WithTimeout(context.WithoutCancel(ctx), queryOnlyCleanupTimeout)
	defer cancel()
	if conn != nil {
		if _, err := conn.ExecContext(cleanupCtx, "PRAGMA query_only = OFF"); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("disable SQLite query_only: %w", err))
		}
		cleanupErr = errors.Join(cleanupErr, conn.Close())
	}
	return errors.Join(queryErr, cleanupErr)
}

func prepareQueryContext(ctx context.Context, queryer platformdb.Queryable, query string, argCount int) (context.Context, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if queryer == nil {
		return nil, errors.New("dbquery queryer is not initialized")
	}
	query = injectLimitIfMissing(query, maxQueryRows)
	if err := validateQuery(query, argCount); err != nil {
		return nil, err
	}
	return ctx, nil
}

func injectLimitIfMissing(query string, max int) string {
	if strings.Contains(strings.ToUpper(maskQuotedStrings(query)), " LIMIT ") {
		return query
	}
	return query + fmt.Sprintf(" LIMIT %d", max)
}

func finalizeQuery(errp *error, finish platformdb.QueryFinish) {
	if finishErr := finish(*errp == nil); finishErr != nil {
		*errp = errors.Join(*errp, finishErr)
	}
}

func withQueryTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = defaultQueryTimeout
	}
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithDeadline(ctx, time.Now().Add(timeout))
}

func validateQuery(query string, argCount int) error {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return errors.New("dbquery query is required")
	}
	if err := validateQueryText(trimmed); err != nil {
		return err
	}
	if err := validatePlaceholders(trimmed, argCount); err != nil {
		return err
	}
	return validateAllowedTables(trimmed)
}

// validateQueryText 校验 SQL 文本只包含单条 SELECT/WITH 查询。
// 注释、分号、写操作关键字和高风险函数都会在执行前被拒绝。
func validateQueryText(query string) error {
	masked := strings.ToLower(maskQuotedStrings(query))
	switch {
	case strings.Contains(masked, "--"), strings.Contains(masked, "/*"), strings.Contains(masked, "*/"):
		return errors.New("dbquery query comments are not allowed")
	case strings.Contains(masked, ";"):
		return errors.New("dbquery query must contain a single statement")
	case !strings.HasPrefix(masked, "select") && !strings.HasPrefix(masked, "with"):
		return errors.New("dbquery only supports SELECT statements")
	case dangerousKeywordPattern.MatchString(masked):
		return errors.New("dbquery query contains disallowed keywords")
	default:
		return nil
	}
}

// validatePlaceholders 校验查询占位符与参数数量一致。
// SQLite `?` 和 PostgreSQL `$n` 不能混用，避免同一查询在不同驱动语义下绑定错位。
func validatePlaceholders(query string, argCount int) error {
	masked := maskQuotedStrings(query)
	dollarMatches := placeholderPattern.FindAllStringSubmatch(masked, -1)
	sqliteCount := len(sqlitePlaceholderPattern.FindAllString(masked, -1))
	switch {
	case len(dollarMatches) > 0 && sqliteCount > 0:
		return errors.New("dbquery query mixes placeholder styles")
	case sqliteCount > 0:
		return validateSQLitePlaceholders(sqliteCount, argCount)
	case len(dollarMatches) > 0:
		return validateDollarPlaceholders(dollarMatches, argCount)
	default:
		return validateNoPlaceholders(argCount)
	}
}

func validateSQLitePlaceholders(placeholderCount, argCount int) error {
	if placeholderCount != argCount {
		return fmt.Errorf("dbquery query expected %d args, got %d", placeholderCount, argCount)
	}
	return nil
}

func validateNoPlaceholders(argCount int) error {
	if argCount == 0 {
		return nil
	}
	return fmt.Errorf("dbquery query expected 0 args, got %d", argCount)
}

// validateDollarPlaceholders 校验 PostgreSQL 风格占位符必须从 $1 连续编号。
// 缺号或最大编号与参数数量不一致都会提前失败，避免参数错位后执行非预期查询。
func validateDollarPlaceholders(matches [][]string, argCount int) error {
	maxIndex := 0
	seen := make(map[int]struct{}, len(matches))
	for _, match := range matches {
		index, err := strconv.Atoi(match[1])
		if err != nil || index <= 0 {
			return fmt.Errorf("dbquery query contains invalid placeholder %q", match[0])
		}
		seen[index] = struct{}{}
		if index > maxIndex {
			maxIndex = index
		}
	}
	if maxIndex != argCount {
		return fmt.Errorf("dbquery query expected %d args, got %d", maxIndex, argCount)
	}
	for index := 1; index <= maxIndex; index++ {
		if _, ok := seen[index]; !ok {
			return fmt.Errorf("dbquery query is missing placeholder $%d", index)
		}
	}
	return nil
}

// validateAllowedTables 校验查询只引用允许暴露给 dbquery 的表。
// CTE 会先被拆出再扫描外层 SELECT，避免用 CTE 名称绕过真实表白名单。
func validateAllowedTables(query string) error {
	masked := strings.ToLower(maskQuotedStrings(query))
	if name := disallowedFunctionName(masked); name != "" {
		return fmt.Errorf("dbquery query calls disallowed function %q", name)
	}
	cteNames, outerQuery, err := collectCTEInfo(query)
	if err != nil {
		return err
	}
	allowedRefs, disallowed := tableReferences(masked, cteNames)
	if len(disallowed) > 0 {
		slices.Sort(disallowed)
		return fmt.Errorf("dbquery query references disallowed tables: %s", strings.Join(disallowed, ", "))
	}
	if allowedRefs == 0 {
		return errors.New("dbquery query must reference at least one allowed table")
	}
	if len(cteNames) > 0 && !hasTableReference(maskQuotedStrings(outerQuery)) {
		return errors.New("dbquery query outer SELECT must reference a table")
	}
	if len(cteNames) > 0 && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(outerQuery)), "select") {
		return errors.New("dbquery WITH query must end in SELECT")
	}
	return nil
}

func disallowedFunctionName(query string) string {
	match := dangerousFunctionCallPattern.FindStringSubmatch(query)
	if len(match) < 2 {
		match = dangerousBareFunctionPattern.FindStringSubmatch(query)
	}
	if len(match) < 1 {
		return ""
	}
	if len(match) >= 2 {
		return strings.ToLower(strings.TrimSpace(match[1]))
	}
	return strings.ToLower(strings.TrimSpace(match[0]))
}

// tableReferences 扫描 FROM/JOIN 中的真实表引用。
// 返回允许表命中数量和违规表名列表，调用方据此要求至少引用一个白名单表。
func tableReferences(query string, cteNames map[string]struct{}) (int, []string) {
	refs := tableReferenceScan{}
	for index := 0; index < len(query); index++ {
		if query[index] == '"' {
			if next, ok := skipQuotedIdentifier(query, index); ok {
				index = next - 1
			}
			continue
		}
		switch {
		case isKeywordAt(query, index, "from"):
			scanFromSources(query, index+len("from"), cteNames, &refs)
		case isKeywordAt(query, index, "join"):
			scanTableSource(query, index+len("join"), cteNames, &refs)
		}
	}
	return refs.allowedRefs, refs.disallowed
}

// hasTableReference 判断查询是否包含 FROM 或 JOIN 表来源。
// 它会跳过双引号标识符，避免把列名或别名里的关键字误判为真实表引用。
func hasTableReference(query string) bool {
	query = strings.ToLower(query)
	for index := 0; index < len(query); index++ {
		if query[index] == '"' {
			if next, ok := skipQuotedIdentifier(query, index); ok {
				index = next - 1
			}
			continue
		}
		if isKeywordAt(query, index, "from") || isKeywordAt(query, index, "join") {
			return true
		}
	}
	return false
}

// tableReferenceScan 收集 SQL 表来源扫描结果。
// allowedRefs 记录命中白名单的表数量，disallowed 保留去重后的违规表名。
type tableReferenceScan struct {
	allowedRefs int
	disallowed  []string
}

func (refs *tableReferenceScan) addTable(name string, cteNames map[string]struct{}) {
	name = normalizeIdentifier(name)
	if name == "" {
		refs.addDisallowed("unknown")
		return
	}
	if _, ok := cteNames[name]; ok {
		return
	}
	if _, ok := allowedTables[name]; ok {
		refs.allowedRefs++
		return
	}
	refs.addDisallowed(name)
}

func (refs *tableReferenceScan) addDisallowed(name string) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		name = "unknown"
	}
	if !slices.Contains(refs.disallowed, name) {
		refs.disallowed = append(refs.disallowed, name)
	}
}

func scanFromSources(query string, index int, cteNames map[string]struct{}, refs *tableReferenceScan) {
	for {
		next := scanTableSource(query, index, cteNames, refs)
		boundaryIndex, boundary := skipTableSourceTail(query, next)
		switch boundary {
		case tableSourceComma:
			index = boundaryIndex + 1
		case tableSourceJoin:
			index = boundaryIndex + len("join")
		default:
			return
		}
	}
}

// scanTableSource 从 FROM/JOIN 后扫描一个表来源。
// CTE 名称会被视为局部来源跳过，带 schema 的限定名和表函数一律按违规来源处理。
func scanTableSource(query string, index int, cteNames map[string]struct{}, refs *tableReferenceScan) int {
	index = skipSpaces(query, index)
	index = skipTableSourcePrefix(query, index)
	if index >= len(query) {
		refs.addDisallowed("unknown")
		return index
	}
	if query[index] == '(' {
		return scanParenthesizedTableSource(query, index, refs)
	}
	name, next, qualified, ok := readQualifiedIdentifier(query, index)
	if !ok {
		refs.addDisallowed("unknown")
		return index + 1
	}
	next = skipSpaces(query, next)
	if qualified {
		refs.addDisallowed(normalizeQualifiedIdentifier(name))
		return skipPossibleTableFunction(query, next)
	}
	if next < len(query) && query[next] == '(' {
		refs.addDisallowed(normalizeIdentifier(name))
		return skipPossibleTableFunction(query, next)
	}
	refs.addTable(name, cteNames)
	return next
}

func scanParenthesizedTableSource(query string, index int, refs *tableReferenceScan) int {
	inner := skipSpaces(query, index+1)
	if !isKeywordAt(query, inner, "select") && !isKeywordAt(query, inner, "with") {
		refs.addDisallowed("parenthesized table expression")
	}
	next, err := skipBalanced(query, index)
	if err != nil {
		refs.addDisallowed("unbalanced table expression")
		return len(query)
	}
	return next
}

func skipTableSourcePrefix(query string, index int) int {
	for {
		index = skipSpaces(query, index)
		switch {
		case isKeywordAt(query, index, "only"):
			index += len("only")
		case isKeywordAt(query, index, "lateral"):
			index += len("lateral")
		default:
			return index
		}
	}
}

func skipPossibleTableFunction(query string, index int) int {
	if index >= len(query) || query[index] != '(' {
		return index
	}
	next, err := skipBalanced(query, index)
	if err != nil {
		return len(query)
	}
	return next
}

type tableSourceBoundary int

const (
	tableSourceEnd tableSourceBoundary = iota
	tableSourceComma
	tableSourceJoin
)

// skipTableSourceTail 跳过表名后的 alias、ON 条件和括号表达式。
// 返回值告诉上层继续扫描逗号表、JOIN，还是在 WHERE 等终止关键字处停止。
func skipTableSourceTail(query string, index int) (int, tableSourceBoundary) {
	for index < len(query) {
		index = skipSpaces(query, index)
		if index >= len(query) {
			break
		}
		switch {
		case query[index] == ',':
			return index, tableSourceComma
		case query[index] == '"':
			next, ok := skipQuotedIdentifier(query, index)
			if !ok {
				return len(query), tableSourceEnd
			}
			index = next
		case query[index] == '(':
			next, err := skipBalanced(query, index)
			if err != nil {
				return len(query), tableSourceEnd
			}
			index = next
		case isKeywordAt(query, index, "join"):
			return index, tableSourceJoin
		case isTableSourceTerminator(query, index):
			return index, tableSourceEnd
		default:
			index++
		}
	}
	return index, tableSourceEnd
}

func isTableSourceTerminator(query string, index int) bool {
	for _, keyword := range []string{"where", "group", "having", "order", "limit", "offset", "union", "intersect", "except", "window"} {
		if isKeywordAt(query, index, keyword) {
			return true
		}
	}
	return false
}

// readQualifiedIdentifier 读取可带点号的 SQL 标识符。
// qualified 标记是否出现 schema/table 形式，调用方据此阻断跨 schema 或函数式来源。
func readQualifiedIdentifier(value string, index int) (string, int, bool, bool) {
	first, next, ok := readIdentifier(value, index)
	if !ok {
		return "", index, false, false
	}
	parts := []string{first}
	qualified := false
	for {
		dot := skipSpaces(value, next)
		if dot >= len(value) || value[dot] != '.' {
			return strings.Join(parts, "."), next, qualified, true
		}
		part, after, ok := readIdentifier(value, skipSpaces(value, dot+1))
		if !ok {
			return strings.Join(parts, "."), dot + 1, true, true
		}
		parts = append(parts, part)
		next = after
		qualified = true
	}
}

func normalizeQualifiedIdentifier(value string) string {
	parts := strings.Split(value, ".")
	for index, part := range parts {
		parts[index] = normalizeIdentifier(part)
	}
	return strings.Join(parts, ".")
}

// isKeywordAt 判断指定位置是否是完整 SQL 关键字。
// 前后字符必须不是标识符字符，避免把 from_id 之类字段名误判为 FROM。
func isKeywordAt(value string, index int, keyword string) bool {
	end := index + len(keyword)
	if index < 0 || end > len(value) {
		return false
	}
	if !strings.EqualFold(value[index:end], keyword) {
		return false
	}
	if index > 0 && isIdentifierPart(value[index-1]) {
		return false
	}
	return end >= len(value) || !isIdentifierPart(value[end])
}

// skipQuotedIdentifier 跳过 SQLite 双引号标识符。
// 它处理 "" 转义，供关键字和表来源扫描避开被引用的列名或别名。
func skipQuotedIdentifier(value string, index int) (int, bool) {
	if index >= len(value) || value[index] != '"' {
		return index, false
	}
	for next := index + 1; next < len(value); next++ {
		if value[next] == '"' {
			if next+1 < len(value) && value[next+1] == '"' {
				next++
				continue
			}
			return next + 1, true
		}
	}
	return len(value), false
}

// maskQuotedStrings 将 SQL 字符串字面量替换为空格。
// 安全检查只关心结构性关键字，不能让用户字符串里的 SELECT、分号或注释符号造成误判。
func maskQuotedStrings(query string) string {
	var builder strings.Builder
	builder.Grow(len(query))
	inQuote := false
	for index := 0; index < len(query); index++ {
		ch := query[index]
		if ch == '\'' {
			builder.WriteByte(' ')
			if inQuote && index+1 < len(query) && query[index+1] == '\'' {
				builder.WriteByte(' ')
				index++
				continue
			}
			inQuote = !inQuote
			continue
		}
		if inQuote {
			builder.WriteByte(' ')
			continue
		}
		builder.WriteByte(ch)
	}
	return builder.String()
}
