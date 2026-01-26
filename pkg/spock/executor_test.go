package spock

import (
	"testing"
)

func TestNewExecutor_Defaults(t *testing.T) {
	config := Config{
		Enabled:         true,
		ReplicationSets: []string{"default"},
		DefaultRepSet:   "default",
	}

	executor := NewExecutor(nil, nil, config)

	// Check that defaults are applied
	if executor.config.MaxWaitAttempts != 30 {
		t.Errorf("NewExecutor() MaxWaitAttempts = %d, want 30", executor.config.MaxWaitAttempts)
	}

	if executor.config.BaseWaitDelayMs != 100 {
		t.Errorf("NewExecutor() BaseWaitDelayMs = %d, want 100", executor.config.BaseWaitDelayMs)
	}

	if executor.config.NodeOffset != 1 {
		t.Errorf("NewExecutor() NodeOffset = %d, want 1", executor.config.NodeOffset)
	}
}

func TestNewExecutor_CustomConfig(t *testing.T) {
	config := Config{
		Enabled:         true,
		ReplicationSets: []string{"default"},
		DefaultRepSet:   "default",
		MaxWaitAttempts: 50,
		BaseWaitDelayMs: 200,
		NodeOffset:      2,
	}

	executor := NewExecutor(nil, nil, config)

	// Check that custom values are preserved
	if executor.config.MaxWaitAttempts != 50 {
		t.Errorf("NewExecutor() MaxWaitAttempts = %d, want 50", executor.config.MaxWaitAttempts)
	}

	if executor.config.BaseWaitDelayMs != 200 {
		t.Errorf("NewExecutor() BaseWaitDelayMs = %d, want 200", executor.config.BaseWaitDelayMs)
	}

	if executor.config.NodeOffset != 2 {
		t.Errorf("NewExecutor() NodeOffset = %d, want 2", executor.config.NodeOffset)
	}
}

func TestTransformStatements(t *testing.T) {
	config := Config{
		Enabled:         true,
		ReplicationSets: []string{"default", "ddl_sql"},
		DefaultRepSet:   "default",
		NodeOffset:      1,
	}

	executor := NewExecutor(nil, nil, config)

	tests := []struct {
		name          string
		statements    []string
		wantDDLCount  int
		wantTableInfo bool
		wantSequences int
	}{
		{
			name:          "single DDL statement",
			statements:    []string{"CREATE TABLE test (id int)"},
			wantDDLCount:  1,
			wantTableInfo: true,
			wantSequences: 0,
		},
		{
			name:          "DDL with SERIAL",
			statements:    []string{"CREATE TABLE users (id SERIAL PRIMARY KEY, name text)"},
			wantDDLCount:  1,
			wantTableInfo: true,
			wantSequences: 1,
		},
		{
			name:          "DML statement",
			statements:    []string{"INSERT INTO test VALUES (1)"},
			wantDDLCount:  0,
			wantTableInfo: false,
		},
		{
			name:          "mixed statements",
			statements:    []string{"CREATE TABLE test (id int)", "INSERT INTO test VALUES (1)"},
			wantDDLCount:  1,
			wantTableInfo: true,
		},
		{
			name:          "empty statements",
			statements:    []string{},
			wantDDLCount:  0,
			wantTableInfo: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := executor.TransformStatements(tt.statements)
			if err != nil {
				t.Fatalf("TransformStatements() error = %v", err)
			}

			ddlCount := 0
			hasTableInfo := false
			seqCount := 0
			for _, stmt := range result {
				if stmt.IsDDL {
					ddlCount++
				}
				if stmt.TableInfo != nil {
					hasTableInfo = true
					seqCount += len(stmt.TableInfo.Sequences)
				}
			}

			if ddlCount != tt.wantDDLCount {
				t.Errorf("TransformStatements() DDL count = %d, want %d", ddlCount, tt.wantDDLCount)
			}

			if hasTableInfo != tt.wantTableInfo {
				t.Errorf("TransformStatements() hasTableInfo = %v, want %v", hasTableInfo, tt.wantTableInfo)
			}

			if tt.wantSequences > 0 && seqCount != tt.wantSequences {
				t.Errorf("TransformStatements() sequence count = %d, want %d", seqCount, tt.wantSequences)
			}
		})
	}
}

func TestTransformStatements_WrapsWithSpock(t *testing.T) {
	config := Config{
		Enabled:         true,
		ReplicationSets: []string{"default"},
		DefaultRepSet:   "default",
	}

	executor := NewExecutor(nil, nil, config)

	statements := []string{"CREATE TABLE test (id int)"}
	result, err := executor.TransformStatements(statements)
	if err != nil {
		t.Fatalf("TransformStatements() error = %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("TransformStatements() returned %d statements, want 1", len(result))
	}

	// Check that DDL is wrapped
	if result[0].Primary == result[0].Original {
		t.Error("TransformStatements() should wrap DDL statements")
	}

	// Check that it contains spock.replicate_ddl
	if !containsString(result[0].Primary, "spock.replicate_ddl") {
		t.Errorf("TransformStatements() Primary should contain spock.replicate_ddl, got: %s", result[0].Primary)
	}

	// Check that it uses $spock_ddl$ tags
	if !containsString(result[0].Primary, "$spock_ddl$") {
		t.Errorf("TransformStatements() Primary should use $spock_ddl$ tags, got: %s", result[0].Primary)
	}
}

func TestTransformStatements_PreservesNonDDL(t *testing.T) {
	config := Config{
		Enabled:         true,
		ReplicationSets: []string{"default"},
		DefaultRepSet:   "default",
	}

	executor := NewExecutor(nil, nil, config)

	statements := []string{"INSERT INTO test VALUES (1)"}
	result, err := executor.TransformStatements(statements)
	if err != nil {
		t.Fatalf("TransformStatements() error = %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("TransformStatements() returned %d statements, want 1", len(result))
	}

	// Check that non-DDL is NOT wrapped
	if result[0].Primary != result[0].Original {
		t.Errorf("TransformStatements() should not wrap non-DDL. Original: %s, Primary: %s",
			result[0].Original, result[0].Primary)
	}

	// Check that it's not marked as DDL
	if result[0].IsDDL {
		t.Error("TransformStatements() should not mark INSERT as DDL")
	}
}

func TestTransformStatements_GeneratesSequenceAlters(t *testing.T) {
	config := Config{
		Enabled:         true,
		ReplicationSets: []string{"default"},
		DefaultRepSet:   "default",
		NodeOffset:      1,
	}

	executor := NewExecutor(nil, nil, config)

	statements := []string{"CREATE TABLE users (id SERIAL PRIMARY KEY, other_id BIGSERIAL)"}
	result, err := executor.TransformStatements(statements)
	if err != nil {
		t.Fatalf("TransformStatements() error = %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("TransformStatements() returned %d statements, want 1", len(result))
	}

	// Check that sequence ALTERs are generated
	if len(result[0].SequenceAlters) != 2 {
		t.Errorf("TransformStatements() should generate 2 sequence ALTERs, got %d", len(result[0].SequenceAlters))
	}

	// Check that ALTER statements contain INCREMENT BY 2 and RESTART WITH 1
	for i, alter := range result[0].SequenceAlters {
		if !containsString(alter, "INCREMENT BY 2") {
			t.Errorf("SequenceAlters[%d] should contain 'INCREMENT BY 2', got: %s", i, alter)
		}
		if !containsString(alter, "RESTART WITH 1") {
			t.Errorf("SequenceAlters[%d] should contain 'RESTART WITH 1', got: %s", i, alter)
		}
	}
}

func TestWaitForTableOnRemote_NilConnection(t *testing.T) {
	config := Config{
		Enabled:         true,
		ReplicationSets: []string{"default"},
	}

	// Create executor with nil remote connection
	executor := NewExecutor(nil, nil, config)

	// Import context for the test
	err := executor.waitForTableOnRemote(nil, "public", "test")
	if err == nil {
		t.Error("waitForTableOnRemote() should return error when remoteConn is nil")
	}

	if !containsString(err.Error(), "remote connection not available") {
		t.Errorf("waitForTableOnRemote() error should mention remote connection, got: %v", err)
	}
}

func TestWaitForSequenceOnRemote_NilConnection(t *testing.T) {
	config := Config{
		Enabled:         true,
		ReplicationSets: []string{"default"},
	}

	executor := NewExecutor(nil, nil, config)

	err := executor.waitForSequenceOnRemote(nil, "public", "test_id_seq")
	if err == nil {
		t.Error("waitForSequenceOnRemote() should return error when remoteConn is nil")
	}

	if !containsString(err.Error(), "remote connection not available") {
		t.Errorf("waitForSequenceOnRemote() error should mention remote connection, got: %v", err)
	}
}

func TestTruncateSQL(t *testing.T) {
	tests := []struct {
		name   string
		sql    string
		maxLen int
		want   string
	}{
		{
			name:   "short string",
			sql:    "SELECT 1",
			maxLen: 20,
			want:   "SELECT 1",
		},
		{
			name:   "exact length",
			sql:    "SELECT 1",
			maxLen: 8,
			want:   "SELECT 1",
		},
		{
			name:   "needs truncation",
			sql:    "CREATE TABLE test (id int)",
			maxLen: 10,
			want:   "CREATE TAB...",
		},
		{
			name:   "empty string",
			sql:    "",
			maxLen: 10,
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateSQL(tt.sql, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncateSQL(%q, %d) = %q, want %q", tt.sql, tt.maxLen, got, tt.want)
			}
		})
	}
}

// Helper function to check string containment
func containsString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
