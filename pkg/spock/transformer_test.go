package spock

import (
	"strings"
	"testing"
)

func TestWrapDDL(t *testing.T) {
	tests := []struct {
		name     string
		stmt     string
		repSets  []string
		wantTags bool // should use $spock_ddl$ tags
		wantSets string
	}{
		{
			name:     "simple CREATE TABLE",
			stmt:     "CREATE TABLE test (id int)",
			repSets:  []string{"default"},
			wantTags: true,
			wantSets: "ARRAY['default']",
		},
		{
			name:     "statement with dollar signs",
			stmt:     "CREATE FUNCTION foo() RETURNS void AS $$ SELECT 1 $$ LANGUAGE sql",
			repSets:  []string{"default"},
			wantTags: true, // should still work because we use $spock_ddl$ tags
		},
		{
			name:     "multiple replication sets",
			stmt:     "CREATE TABLE test (id int)",
			repSets:  []string{"default", "ddl_sql"},
			wantTags: true,
			wantSets: "ARRAY['default', 'ddl_sql']",
		},
		{
			name:     "empty replication sets",
			stmt:     "DROP TABLE test",
			repSets:  []string{},
			wantTags: true,
			wantSets: "ARRAY[]::text[]",
		},
		{
			name:     "statement with single quotes",
			stmt:     "INSERT INTO test VALUES ('hello')",
			repSets:  []string{"default"},
			wantTags: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := WrapDDL(tt.stmt, tt.repSets)

			// Check that it uses the unique dollar-quote tag
			if tt.wantTags {
				if !strings.Contains(result, "$spock_ddl$") {
					t.Errorf("WrapDDL() should use $spock_ddl$ tags, got: %s", result)
				}
			}

			// Check that the original statement is preserved
			if !strings.Contains(result, tt.stmt) {
				t.Errorf("WrapDDL() should contain original statement, got: %s", result)
			}

			// Check array formatting if expected
			if tt.wantSets != "" && !strings.Contains(result, tt.wantSets) {
				t.Errorf("WrapDDL() should contain %s, got: %s", tt.wantSets, result)
			}

			// Check it starts with SELECT spock.replicate_ddl
			if !strings.HasPrefix(result, "SELECT spock.replicate_ddl(") {
				t.Errorf("WrapDDL() should start with SELECT spock.replicate_ddl(, got: %s", result)
			}
		})
	}
}

func TestWrapDDL_NoDollarEscapeBug(t *testing.T) {
	// Specifically test that we don't produce invalid $\$ escaping
	stmt := "CREATE FUNCTION test() AS $$ SELECT 1 $$ LANGUAGE sql"
	result := WrapDDL(stmt, []string{"default"})

	// Should NOT contain the invalid escape sequence
	if strings.Contains(result, "$\\$") {
		t.Errorf("WrapDDL() should not produce invalid $\\$ escaping, got: %s", result)
	}

	// Should contain the original $$ unmodified
	if !strings.Contains(result, "$$") {
		t.Errorf("WrapDDL() should preserve $$ in statement, got: %s", result)
	}
}

func TestGenerateAddTableSQL(t *testing.T) {
	tests := []struct {
		name     string
		repSet   string
		schema   string
		table    string
		syncData bool
		want     string
	}{
		{
			name:     "basic table",
			repSet:   "default",
			schema:   "public",
			table:    "users",
			syncData: true,
			want:     "SELECT spock.repset_add_table('default', '\"public\".\"users\"', true)",
		},
		{
			name:     "sync disabled",
			repSet:   "default",
			schema:   "public",
			table:    "logs",
			syncData: false,
			want:     "SELECT spock.repset_add_table('default', '\"public\".\"logs\"', false)",
		},
		{
			name:     "quoted identifiers",
			repSet:   "my-set",
			schema:   "my_schema",
			table:    "My_Table",
			syncData: true,
			want:     "SELECT spock.repset_add_table('my-set', '\"my_schema\".\"My_Table\"', true)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateAddTableSQL(tt.repSet, tt.schema, tt.table, tt.syncData)
			if got != tt.want {
				t.Errorf("GenerateAddTableSQL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatArrayLiteral(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   string
	}{
		{
			name:   "single value",
			values: []string{"default"},
			want:   "ARRAY['default']",
		},
		{
			name:   "multiple values",
			values: []string{"default", "ddl_sql", "custom"},
			want:   "ARRAY['default', 'ddl_sql', 'custom']",
		},
		{
			name:   "empty array",
			values: []string{},
			want:   "ARRAY[]::text[]",
		},
		{
			name:   "values with quotes",
			values: []string{"it's", "test'value"},
			want:   "ARRAY['it''s', 'test''value']",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatArrayLiteral(tt.values)
			if got != tt.want {
				t.Errorf("formatArrayLiteral() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestQuoteIdent(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"users", `"users"`},
		{"my_table", `"my_table"`},
		{`already"quoted`, `"already""quoted"`}, // double quotes escaped
		{"MixedCase", `"MixedCase"`},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := quoteIdent(tt.input)
			if got != tt.want {
				t.Errorf("quoteIdent(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestQuoteLiteral(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "'hello'"},
		{"it's", "'it''s'"}, // single quotes escaped
		{"test'value", "'test''value'"},
		{"no quotes", "'no quotes'"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := quoteLiteral(tt.input)
			if got != tt.want {
				t.Errorf("quoteLiteral(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
