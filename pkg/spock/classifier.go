package spock

import (
	"regexp"
	"strings"
)

var (
	ddlPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)^\s*CREATE\s+`),
		regexp.MustCompile(`(?i)^\s*ALTER\s+`),
		regexp.MustCompile(`(?i)^\s*DROP\s+`),
		regexp.MustCompile(`(?i)^\s*GRANT\s+`),
		regexp.MustCompile(`(?i)^\s*REVOKE\s+`),
		regexp.MustCompile(`(?i)^\s*TRUNCATE\s+`),
	}

	// Pattern to extract table info from CREATE TABLE statements
	// Handles: CREATE TABLE, CREATE UNLOGGED TABLE, CREATE TABLE IF NOT EXISTS
	// With optional schema prefix
	createTablePattern = regexp.MustCompile(
		`(?i)^\s*CREATE\s+(?:UNLOGGED\s+)?TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?` +
			`(?:"?([a-zA-Z_][a-zA-Z0-9_]*)"?\s*\.\s*)?` +
			`"?([a-zA-Z_][a-zA-Z0-9_]*)"?`,
	)
)

// stripLeadingComments removes leading SQL comments (-- and /* */) from a statement
func stripLeadingComments(stmt string) string {
	lines := strings.Split(stmt, "\n")
	result := make([]string, 0, len(lines))

	inBlockComment := false
	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)
		lineToAdd := line // Track which line to add (original or modified)

		// Handle block comments
		if inBlockComment {
			if idx := strings.Index(trimmedLine, "*/"); idx >= 0 {
				inBlockComment = false
				trimmedLine = strings.TrimSpace(trimmedLine[idx+2:])
				if trimmedLine == "" {
					continue
				}
				lineToAdd = trimmedLine // Use processed content
			} else {
				continue
			}
		}

		// Skip empty lines at the beginning
		if trimmedLine == "" && len(result) == 0 {
			continue
		}

		// Skip single-line comments at the beginning
		if strings.HasPrefix(trimmedLine, "--") && len(result) == 0 {
			continue
		}

		// Handle block comment start
		if strings.HasPrefix(trimmedLine, "/*") && len(result) == 0 {
			if idx := strings.Index(trimmedLine, "*/"); idx >= 0 {
				// Block comment on single line
				trimmedLine = strings.TrimSpace(trimmedLine[idx+2:])
				if trimmedLine == "" {
					continue
				}
				lineToAdd = trimmedLine // Use processed content
			} else {
				inBlockComment = true
				continue
			}
		}

		result = append(result, lineToAdd)
	}

	return strings.Join(result, "\n")
}

// ClassifyStatement determines the type of SQL statement
func ClassifyStatement(stmt string) StatementType {
	// Strip leading comments before classification
	stripped := stripLeadingComments(stmt)
	trimmed := strings.TrimSpace(stripped)

	for _, pattern := range ddlPatterns {
		if pattern.MatchString(trimmed) {
			return StatementDDL
		}
	}

	// Check for DML statements
	dmlPatterns := []string{"INSERT", "UPDATE", "DELETE"}
	upperTrimmed := strings.ToUpper(trimmed)
	for _, dml := range dmlPatterns {
		if strings.HasPrefix(upperTrimmed, dml) {
			return StatementDML
		}
	}

	return StatementOther
}

// ExtractTableInfo extracts schema and table name from CREATE TABLE statements
func ExtractTableInfo(stmt string) *TableInfo {
	// Strip leading comments before extracting table info
	stripped := stripLeadingComments(stmt)
	matches := createTablePattern.FindStringSubmatch(stripped)
	if len(matches) < 3 {
		return nil
	}

	schema := matches[1]
	if schema == "" {
		schema = "public"
	}

	tableName := matches[2]
	if tableName == "" {
		return nil
	}

	return &TableInfo{
		Schema: schema,
		Name:   tableName,
	}
}

// IsDDL returns true if the statement is a DDL statement
func IsDDL(stmt string) bool {
	return ClassifyStatement(stmt) == StatementDDL
}

// IsDML returns true if the statement is a DML statement
func IsDML(stmt string) bool {
	return ClassifyStatement(stmt) == StatementDML
}

// isTransactionControl returns true if the statement is a transaction control statement
// (BEGIN, COMMIT, ROLLBACK, END). These are skipped in Spock mode because each DDL
// statement is executed individually, and transaction wrappers in migration files would
// prevent DDL from being replicated to the remote before COMMIT.
func isTransactionControl(stmt string) bool {
	stripped := stripLeadingComments(stmt)
	upper := strings.ToUpper(strings.TrimSpace(stripped))
	return upper == "BEGIN" || strings.HasPrefix(upper, "BEGIN;") ||
		upper == "COMMIT" || strings.HasPrefix(upper, "COMMIT;") ||
		upper == "ROLLBACK" || strings.HasPrefix(upper, "ROLLBACK;") ||
		upper == "END" || strings.HasPrefix(upper, "END;")
}
