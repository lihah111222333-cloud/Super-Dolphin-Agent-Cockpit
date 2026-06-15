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

// executeQuery 执行查询。
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

type sqliteReadOnlyConnector interface {
	Conn(context.Context) (*sql.Conn, error)
}

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

// validateQueryText 校验查询文本。
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

// validatePlaceholders 校验placeholders。
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

// validateAllowedTables 校验allowedtables。
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

// tableReferences 处理table引用。
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

// maskQuotedStrings 处理maskquotedstrings。
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
