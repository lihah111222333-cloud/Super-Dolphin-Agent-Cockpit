package dbquery

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

const maxQueryRows = 10000

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
	dangerousKeywordPattern      = regexp.MustCompile(`(?i)\b(insert|update|delete|drop|alter|truncate|create|grant|revoke|comment|vacuum|analyze|copy|merge|call|do)\b`)
	dangerousFunctionCallPattern = regexp.MustCompile(`(?i)\b(pg_sleep|pg_terminate_backend|pg_cancel_backend|set_config|version|current_setting|inet_server_addr|inet_server_port|pg_read_file|pg_read_binary_file|pg_ls_dir|pg_stat_\w+|lo_import|lo_export)\b\s*\(`)
	dangerousBareFunctionPattern = regexp.MustCompile(`(?i)\bcurrent_user\b`)
	placeholderPattern           = regexp.MustCompile(`\$(\d+)`)
	tableReferencePattern        = regexp.MustCompile(`(?i)\b(?:from|join)\s+(?:only\s+|lateral\s+)?((?:"[^"]+"|\w+)(?:\.(?:"[^"]+"|\w+))?)`)
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
	rows, finish, err := platformdb.OpenReadOnlyRows(queryCtx, queryer, query, normalizeArgs(args)...)
	if err != nil {
		return nil, err
	}
	defer finalizeQuery(&err, finish)
	fields := platformdb.RowsFieldNames(rows)
	result := make([]map[string]any, 0)
	for rows.Next() {
		if len(result) >= maxQueryRows {
			return nil, fmt.Errorf("dbquery query exceeded row limit %d", maxQueryRows)
		}
		values, err := rows.Values()
		if err != nil {
			return nil, err
		}
		result = append(result, rowValues(fields, values))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
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
	matches := placeholderPattern.FindAllStringSubmatch(query, -1)
	if len(matches) == 0 {
		if argCount == 0 {
			return nil
		}
		return fmt.Errorf("dbquery query expected 0 args, got %d", argCount)
	}
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
	matches := tableReferencePattern.FindAllStringSubmatch(query, -1)
	allowedRefs := 0
	disallowed := make([]string, 0)
	for _, match := range matches {
		name := normalizeIdentifier(match[1])
		if name == "" {
			continue
		}
		if _, ok := cteNames[name]; ok {
			continue
		}
		if _, ok := allowedTables[name]; ok {
			allowedRefs++
			continue
		}
		if !slices.Contains(disallowed, name) {
			disallowed = append(disallowed, name)
		}
	}
	return allowedRefs, disallowed
}

func hasTableReference(query string) bool {
	return tableReferencePattern.MatchString(strings.ToLower(query))
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
