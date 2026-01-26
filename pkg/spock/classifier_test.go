package spock

import (
	"testing"
)

func TestIsDDL(t *testing.T) {
	tests := []struct {
		name string
		stmt string
		want bool
	}{
		// CREATE statements
		{"CREATE TABLE basic", "CREATE TABLE test (id int)", true},
		{"CREATE TABLE if not exists", "CREATE TABLE IF NOT EXISTS test (id int)", true},
		{"CREATE TABLE unlogged", "CREATE UNLOGGED TABLE test (id int)", true},
		{"CREATE TABLE with schema", "CREATE TABLE myschema.test (id int)", true},
		{"CREATE INDEX", "CREATE INDEX idx ON test (col)", true},
		{"CREATE VIEW", "CREATE VIEW myview AS SELECT 1", true},
		{"CREATE FUNCTION", "CREATE FUNCTION foo() RETURNS void AS $$ $$ LANGUAGE sql", true},
		{"CREATE TRIGGER", "CREATE TRIGGER trig BEFORE INSERT ON test FOR EACH ROW EXECUTE FUNCTION foo()", true},
		{"CREATE SCHEMA", "CREATE SCHEMA myschema", true},

		// ALTER statements
		{"ALTER TABLE", "ALTER TABLE test ADD COLUMN col int", true},
		{"ALTER INDEX", "ALTER INDEX idx RENAME TO idx2", true},
		{"ALTER SEQUENCE", "ALTER SEQUENCE test_id_seq RESTART WITH 1", true},

		// DROP statements
		{"DROP TABLE", "DROP TABLE test", true},
		{"DROP TABLE if exists", "DROP TABLE IF EXISTS test", true},
		{"DROP INDEX", "DROP INDEX idx", true},

		// GRANT/REVOKE
		{"GRANT", "GRANT SELECT ON test TO user1", true},
		{"REVOKE", "REVOKE SELECT ON test FROM user1", true},

		// TRUNCATE
		{"TRUNCATE", "TRUNCATE test", true},
		{"TRUNCATE CASCADE", "TRUNCATE test CASCADE", true},

		// DML statements (not DDL)
		{"INSERT", "INSERT INTO test VALUES (1)", false},
		{"UPDATE", "UPDATE test SET col = 1", false},
		{"DELETE", "DELETE FROM test WHERE id = 1", false},

		// Other statements (not DDL)
		{"SELECT", "SELECT * FROM test", false},
		{"SELECT with create", "SELECT create_function()", false}, // contains CREATE but isn't DDL

		// Whitespace variations
		{"leading spaces", "   CREATE TABLE test (id int)", true},
		{"leading newlines", "\n\nCREATE TABLE test (id int)", true},
		{"leading tabs", "\t\tCREATE TABLE test (id int)", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsDDL(tt.stmt)
			if got != tt.want {
				t.Errorf("IsDDL(%q) = %v, want %v", tt.stmt, got, tt.want)
			}
		})
	}
}

func TestIsDML(t *testing.T) {
	tests := []struct {
		name string
		stmt string
		want bool
	}{
		{"INSERT", "INSERT INTO test VALUES (1)", true},
		{"UPDATE", "UPDATE test SET col = 1", true},
		{"DELETE", "DELETE FROM test", true},
		{"SELECT", "SELECT * FROM test", false},
		{"CREATE TABLE", "CREATE TABLE test (id int)", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsDML(tt.stmt)
			if got != tt.want {
				t.Errorf("IsDML(%q) = %v, want %v", tt.stmt, got, tt.want)
			}
		})
	}
}

func TestClassifyStatement(t *testing.T) {
	tests := []struct {
		name string
		stmt string
		want StatementType
	}{
		{"DDL CREATE", "CREATE TABLE test (id int)", StatementDDL},
		{"DDL ALTER", "ALTER TABLE test ADD col int", StatementDDL},
		{"DDL DROP", "DROP TABLE test", StatementDDL},
		{"DML INSERT", "INSERT INTO test VALUES (1)", StatementDML},
		{"DML UPDATE", "UPDATE test SET col = 1", StatementDML},
		{"DML DELETE", "DELETE FROM test", StatementDML},
		{"Other SELECT", "SELECT * FROM test", StatementOther},
		{"Other EXPLAIN", "EXPLAIN SELECT * FROM test", StatementOther},
		{"Other empty", "", StatementOther},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyStatement(tt.stmt)
			if got != tt.want {
				t.Errorf("ClassifyStatement(%q) = %v, want %v", tt.stmt, got, tt.want)
			}
		})
	}
}

func TestExtractTableInfo(t *testing.T) {
	tests := []struct {
		name       string
		stmt       string
		wantSchema string
		wantTable  string
		wantNil    bool
	}{
		{
			name:       "basic CREATE TABLE",
			stmt:       "CREATE TABLE test (id int)",
			wantSchema: "public",
			wantTable:  "test",
		},
		{
			name:       "CREATE TABLE with schema",
			stmt:       "CREATE TABLE myschema.test (id int)",
			wantSchema: "myschema",
			wantTable:  "test",
		},
		{
			name:       "CREATE TABLE IF NOT EXISTS",
			stmt:       "CREATE TABLE IF NOT EXISTS test (id int)",
			wantSchema: "public",
			wantTable:  "test",
		},
		{
			name:       "CREATE UNLOGGED TABLE",
			stmt:       "CREATE UNLOGGED TABLE test (id int)",
			wantSchema: "public",
			wantTable:  "test",
		},
		{
			name:       "CREATE UNLOGGED TABLE IF NOT EXISTS with schema",
			stmt:       "CREATE UNLOGGED TABLE IF NOT EXISTS myschema.test (id int)",
			wantSchema: "myschema",
			wantTable:  "test",
		},
		{
			name:       "quoted table name",
			stmt:       `CREATE TABLE "MyTable" (id int)`,
			wantSchema: "public",
			wantTable:  "MyTable",
		},
		{
			name:       "quoted schema and table",
			stmt:       `CREATE TABLE "MySchema"."MyTable" (id int)`,
			wantSchema: "MySchema",
			wantTable:  "MyTable",
		},
		{
			name:    "ALTER TABLE (not CREATE)",
			stmt:    "ALTER TABLE test ADD col int",
			wantNil: true,
		},
		{
			name:    "SELECT statement",
			stmt:    "SELECT * FROM test",
			wantNil: true,
		},
		{
			name:    "CREATE INDEX (not TABLE)",
			stmt:    "CREATE INDEX idx ON test (col)",
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractTableInfo(tt.stmt)

			if tt.wantNil {
				if got != nil {
					t.Errorf("ExtractTableInfo(%q) = %+v, want nil", tt.stmt, got)
				}
				return
			}

			if got == nil {
				t.Errorf("ExtractTableInfo(%q) = nil, want schema=%q table=%q",
					tt.stmt, tt.wantSchema, tt.wantTable)
				return
			}

			if got.Schema != tt.wantSchema {
				t.Errorf("ExtractTableInfo(%q).Schema = %q, want %q",
					tt.stmt, got.Schema, tt.wantSchema)
			}

			if got.Name != tt.wantTable {
				t.Errorf("ExtractTableInfo(%q).Name = %q, want %q",
					tt.stmt, got.Name, tt.wantTable)
			}
		})
	}
}

func TestStripLeadingComments(t *testing.T) {
	tests := []struct {
		name string
		stmt string
		want string
	}{
		{
			name: "no comments",
			stmt: "CREATE TABLE test (id int)",
			want: "CREATE TABLE test (id int)",
		},
		{
			name: "single line comment",
			stmt: "-- comment\nCREATE TABLE test (id int)",
			want: "CREATE TABLE test (id int)",
		},
		{
			name: "multiple single line comments",
			stmt: "-- comment 1\n-- comment 2\nCREATE TABLE test (id int)",
			want: "CREATE TABLE test (id int)",
		},
		{
			name: "block comment",
			stmt: "/* block comment */\nCREATE TABLE test (id int)",
			want: "CREATE TABLE test (id int)",
		},
		{
			name: "multiline block comment",
			stmt: "/* block\ncomment */\nCREATE TABLE test (id int)",
			want: "CREATE TABLE test (id int)",
		},
		{
			name: "mixed comments",
			stmt: "-- line comment\n/* block */\nCREATE TABLE test (id int)",
			want: "CREATE TABLE test (id int)",
		},
		{
			name: "inline block comment",
			stmt: "/* comment */ CREATE TABLE test (id int)",
			want: "CREATE TABLE test (id int)",
		},
		{
			name: "empty lines before",
			stmt: "\n\n\nCREATE TABLE test (id int)",
			want: "CREATE TABLE test (id int)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripLeadingComments(tt.stmt)
			if got != tt.want {
				t.Errorf("stripLeadingComments() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractTableInfo_WithComments(t *testing.T) {
	tests := []struct {
		name       string
		stmt       string
		wantSchema string
		wantTable  string
	}{
		{
			name:       "line comment before CREATE",
			stmt:       "-- Create users table\nCREATE TABLE users (id int)",
			wantSchema: "public",
			wantTable:  "users",
		},
		{
			name:       "block comment before CREATE",
			stmt:       "/* Migration 001 */\nCREATE TABLE users (id int)",
			wantSchema: "public",
			wantTable:  "users",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractTableInfo(tt.stmt)
			if got == nil {
				t.Errorf("ExtractTableInfo(%q) = nil", tt.stmt)
				return
			}
			if got.Schema != tt.wantSchema || got.Name != tt.wantTable {
				t.Errorf("ExtractTableInfo(%q) = {%q, %q}, want {%q, %q}",
					tt.stmt, got.Schema, got.Name, tt.wantSchema, tt.wantTable)
			}
		})
	}
}

func TestIsDDL_WithComments(t *testing.T) {
	tests := []struct {
		name string
		stmt string
		want bool
	}{
		{
			name: "DDL with line comment",
			stmt: "-- Create table\nCREATE TABLE test (id int)",
			want: true,
		},
		{
			name: "DDL with block comment",
			stmt: "/* Migration */ CREATE TABLE test (id int)",
			want: true,
		},
		{
			name: "DML with comment",
			stmt: "-- Insert data\nINSERT INTO test VALUES (1)",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsDDL(tt.stmt)
			if got != tt.want {
				t.Errorf("IsDDL(%q) = %v, want %v", tt.stmt, got, tt.want)
			}
		})
	}
}
