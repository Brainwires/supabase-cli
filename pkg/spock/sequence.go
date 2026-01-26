package spock

import (
	"fmt"
	"regexp"
	"strings"
)

// Patterns for detecting SERIAL columns in CREATE TABLE statements
var (
	// Matches column definitions with SERIAL types
	// Captures: column_name, serial_type (SERIAL, BIGSERIAL, SMALLSERIAL)
	// Uses word boundary and lookahead to avoid consuming delimiters
	serialColumnPattern = regexp.MustCompile(
		`(?i)"?([a-zA-Z_][a-zA-Z0-9_]*)"?\s+(SMALLSERIAL|SERIAL|BIGSERIAL)\b`,
	)

	// Matches GENERATED ... AS IDENTITY columns (alternative to SERIAL)
	identityColumnPattern = regexp.MustCompile(
		`(?i)"?([a-zA-Z_][a-zA-Z0-9_]*)"?\s+(?:SMALLINT|INTEGER|INT|BIGINT)\s+GENERATED\s+(?:ALWAYS|BY\s+DEFAULT)\s+AS\s+IDENTITY`,
	)
)

// ExtractSequences extracts sequence information from a CREATE TABLE statement
func ExtractSequences(stmt string, tableInfo *TableInfo) []SequenceInfo {
	if tableInfo == nil {
		return nil
	}

	var sequences []SequenceInfo

	// Extract column body from CREATE TABLE (between first ( and last ))
	columnBody := extractColumnBody(stmt)
	if columnBody == "" {
		return nil
	}

	// Find SERIAL columns
	matches := serialColumnPattern.FindAllStringSubmatch(columnBody, -1)
	for _, match := range matches {
		if len(match) >= 3 {
			columnName := strings.Trim(match[1], `"`)
			serialType := strings.ToUpper(match[2])
			sequences = append(sequences, SequenceInfo{
				Schema:    tableInfo.Schema,
				Name:      fmt.Sprintf("%s_%s_seq", tableInfo.Name, columnName),
				Column:    columnName,
				DataType:  serialType,
				TableName: tableInfo.Name,
			})
		}
	}

	// Find IDENTITY columns (these also create sequences)
	identityMatches := identityColumnPattern.FindAllStringSubmatch(columnBody, -1)
	for _, match := range identityMatches {
		if len(match) >= 2 {
			columnName := strings.Trim(match[1], `"`)
			sequences = append(sequences, SequenceInfo{
				Schema:    tableInfo.Schema,
				Name:      fmt.Sprintf("%s_%s_seq", tableInfo.Name, columnName),
				Column:    columnName,
				DataType:  "IDENTITY",
				TableName: tableInfo.Name,
			})
		}
	}

	return sequences
}

// extractColumnBody extracts the column definitions from a CREATE TABLE statement
func extractColumnBody(stmt string) string {
	// Find the first opening parenthesis
	start := strings.Index(stmt, "(")
	if start == -1 {
		return ""
	}

	// Find the matching closing parenthesis
	depth := 0
	for i := start; i < len(stmt); i++ {
		switch stmt[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return stmt[start+1 : i]
			}
		}
	}

	return ""
}

// GenerateSequenceAlterSQL generates ALTER SEQUENCE statements for bi-directional replication
// The nodeOffset determines the starting value (1 for primary, 2 for standby)
// All sequences are set to INCREMENT BY 2 to interleave IDs between nodes
func GenerateSequenceAlterSQL(seq SequenceInfo, nodeOffset int) string {
	fullSeqName := fmt.Sprintf("%s.%s", quoteIdent(seq.Schema), quoteIdent(seq.Name))
	return fmt.Sprintf(
		"ALTER SEQUENCE %s INCREMENT BY 2 RESTART WITH %d",
		fullSeqName,
		nodeOffset,
	)
}

// GenerateAllSequenceAlters generates ALTER SEQUENCE statements for all sequences in a table
func GenerateAllSequenceAlters(tableInfo *TableInfo, nodeOffset int) []string {
	if tableInfo == nil || len(tableInfo.Sequences) == 0 {
		return nil
	}

	alters := make([]string, 0, len(tableInfo.Sequences))
	for _, seq := range tableInfo.Sequences {
		alters = append(alters, GenerateSequenceAlterSQL(seq, nodeOffset))
	}
	return alters
}

// WrapSequenceAlterDDL wraps a sequence ALTER statement for Spock replication
func WrapSequenceAlterDDL(alterSQL string, repSets []string) string {
	return WrapDDL(alterSQL, repSets)
}
