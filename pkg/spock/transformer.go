package spock

import (
	"fmt"
	"strings"
)

// WrapDDL wraps a DDL statement with spock.replicate_ddl()
func WrapDDL(stmt string, repSets []string) string {
	repSetArray := formatArrayLiteral(repSets)
	// Use unique dollar-quote tag to avoid conflicts with $$ in the statement
	return fmt.Sprintf("SELECT spock.replicate_ddl($spock_ddl$%s$spock_ddl$, %s)", stmt, repSetArray)
}

// GenerateAddTableSQL generates SQL to add a table to a replication set
func GenerateAddTableSQL(repSet, schema, table string, syncData bool) string {
	fullName := fmt.Sprintf("%s.%s", quoteIdent(schema), quoteIdent(table))
	return fmt.Sprintf(
		"SELECT spock.repset_add_table(%s, %s, %t)",
		quoteLiteral(repSet),
		quoteLiteral(fullName),
		syncData,
	)
}

// formatArrayLiteral formats a slice of strings as a PostgreSQL array literal
func formatArrayLiteral(values []string) string {
	if len(values) == 0 {
		return "ARRAY[]::text[]"
	}
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = quoteLiteral(v)
	}
	return fmt.Sprintf("ARRAY[%s]", strings.Join(quoted, ", "))
}

// quoteIdent quotes an identifier (schema, table, column name)
func quoteIdent(s string) string {
	return fmt.Sprintf(`"%s"`, strings.ReplaceAll(s, `"`, `""`))
}

// quoteLiteral quotes a string literal for use in SQL
func quoteLiteral(s string) string {
	return fmt.Sprintf("'%s'", strings.ReplaceAll(s, "'", "''"))
}
