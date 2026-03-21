package dbquery

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type rowQueryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

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
		"task_acks":              {},
		"task_dag_nodes":         {},
		"task_dag_wakeups":       {},
		"task_dag_worker_leases": {},
		"task_dags":              {},
		"task_traces":            {},
		"topology_approvals":     {},
		"ui_preferences":         {},
		"workspace_run_files":    {},
		"workspace_runs":         {},
	}
	dangerousKeywordPattern  = regexp.MustCompile(`(?i)\b(insert|update|delete|drop|alter|truncate|create|grant|revoke|comment|vacuum|analyze|copy|merge|call|do)\b`)
	dangerousFunctionPattern = regexp.MustCompile(`(?i)\b(pg_sleep|pg_terminate_backend|pg_cancel_backend|set_config|version|current_setting|current_user|inet_server_addr|inet_server_port|pg_read_file|pg_read_binary_file|pg_ls_dir|pg_stat_\w+|lo_import|lo_export)\b(?:\s*\(|\b)`)
	placeholderPattern       = regexp.MustCompile(`\$(\d+)`)
	tableReferencePattern    = regexp.MustCompile(`(?i)\b(?:from|join)\s+(?:only\s+|lateral\s+)?((?:"[^"]+"|\w+)(?:\.(?:"[^"]+"|\w+))?)`)
)

func executeQuery(ctx context.Context, queryer rowQueryer, query string, args ...any) ([]map[string]any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if queryer == nil {
		return nil, errors.New("dbquery queryer is not initialized")
	}
	if err := validateQuery(query, len(args)); err != nil {
		return nil, err
	}
	queryCtx, cancel := platformconfig.WithDBQueryTimeout(ctx)
	defer cancel()
	rows, err := queryer.Query(queryCtx, query, normalizeArgs(args)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fields := rows.FieldDescriptions()
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
	match := dangerousFunctionPattern.FindStringSubmatch(query)
	if len(match) < 2 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(match[1]))
}

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

func collectCTEInfo(query string) (map[string]struct{}, string, error) {
	names := make(map[string]struct{})
	trimmed := strings.TrimSpace(query)
	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, "with") {
		return names, trimmed, nil
	}
	index := len("with")
	index = skipSpaces(trimmed, index)
	if strings.HasPrefix(strings.ToLower(trimmed[index:]), "recursive") {
		index += len("recursive")
	}
	for {
		var ok bool
		index = skipSpaces(trimmed, index)
		name, next, ok := readIdentifier(trimmed, index)
		if !ok {
			return nil, "", errors.New("dbquery query has invalid CTE syntax")
		}
		names[normalizeIdentifier(name)] = struct{}{}
		index = skipSpaces(trimmed, next)
		if index < len(trimmed) && trimmed[index] == '(' {
			var err error
			index, err = skipBalanced(trimmed, index)
			if err != nil {
				return nil, "", err
			}
			index = skipSpaces(trimmed, index)
		}
		if !strings.HasPrefix(strings.ToLower(trimmed[index:]), "as") {
			return nil, "", errors.New("dbquery query has invalid CTE syntax")
		}
		index += len("as")
		index = skipSpaces(trimmed, index)
		index = skipMaterialized(trimmed, index)
		if index >= len(trimmed) || trimmed[index] != '(' {
			return nil, "", errors.New("dbquery query has invalid CTE syntax")
		}
		var err error
		index, err = skipBalanced(trimmed, index)
		if err != nil {
			return nil, "", err
		}
		index = skipSpaces(trimmed, index)
		if index >= len(trimmed) {
			return nil, "", errors.New("dbquery query has invalid CTE syntax")
		}
		if trimmed[index] != ',' {
			return names, strings.TrimSpace(trimmed[index:]), nil
		}
		index++
	}
}

func rowValues(fields []pgconn.FieldDescription, values []any) map[string]any {
	row := make(map[string]any, len(fields))
	for index, field := range fields {
		if index >= len(values) {
			break
		}
		row[string(field.Name)] = values[index]
	}
	return row
}

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

func skipSpaces(value string, index int) int {
	for index < len(value) {
		switch value[index] {
		case ' ', '\t', '\n', '\r':
			index++
		default:
			return index
		}
	}
	return index
}

func skipMaterialized(value string, index int) int {
	lower := strings.ToLower(value[index:])
	switch {
	case strings.HasPrefix(lower, "not materialized"):
		return skipSpaces(value, index+len("not materialized"))
	case strings.HasPrefix(lower, "materialized"):
		return skipSpaces(value, index+len("materialized"))
	default:
		return index
	}
}

func readIdentifier(value string, index int) (string, int, bool) {
	if index >= len(value) {
		return "", index, false
	}
	if value[index] == '"' {
		for next := index + 1; next < len(value); next++ {
			if value[next] == '"' {
				return value[index : next+1], next + 1, true
			}
		}
		return "", index, false
	}
	if !isIdentifierStart(value[index]) {
		return "", index, false
	}
	next := index + 1
	for next < len(value) && isIdentifierPart(value[next]) {
		next++
	}
	return value[index:next], next, true
}

func skipBalanced(value string, index int) (int, error) {
	depth := 0
	inSingleQuote := false
	inDoubleQuote := false
	for ; index < len(value); index++ {
		ch := value[index]
		switch {
		case inSingleQuote:
			if ch == '\'' {
				if index+1 < len(value) && value[index+1] == '\'' {
					index++
					continue
				}
				inSingleQuote = false
			}
		case inDoubleQuote:
			if ch == '"' {
				inDoubleQuote = false
			}
		case ch == '\'':
			inSingleQuote = true
		case ch == '"':
			inDoubleQuote = true
		case ch == '(':
			depth++
		case ch == ')':
			depth--
			if depth == 0 {
				return index + 1, nil
			}
		}
	}
	return 0, errors.New("dbquery query has unbalanced parentheses")
}

func normalizeIdentifier(value string) string {
	parts := strings.Split(strings.TrimSpace(value), ".")
	name := parts[len(parts)-1]
	return strings.ToLower(strings.Trim(name, `"`))
}

func isIdentifierStart(ch byte) bool {
	return ch == '_' || ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z'
}

func isIdentifierPart(ch byte) bool {
	return isIdentifierStart(ch) || ch >= '0' && ch <= '9'
}
