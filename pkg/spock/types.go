package spock

// StatementType represents the type of SQL statement
type StatementType int

const (
	StatementUnknown StatementType = iota
	StatementDDL                   // CREATE, ALTER, DROP, GRANT, REVOKE, TRUNCATE
	StatementDML                   // INSERT, UPDATE, DELETE
	StatementOther                 // SELECT, etc.
)

// TableInfo contains schema and table name information
type TableInfo struct {
	Schema    string
	Name      string
	Sequences []SequenceInfo // Sequences associated with this table
}

// SequenceInfo contains information about a sequence (typically from SERIAL columns)
type SequenceInfo struct {
	Schema     string // Schema where the sequence lives
	Name       string // Sequence name (typically table_column_seq)
	Column     string // Column name that uses this sequence
	DataType   string // SERIAL, BIGSERIAL, or SMALLSERIAL
	TableName  string // Table that owns the sequence
}

// Config holds the Spock replication configuration
type Config struct {
	Enabled         bool
	RemoteDSN       string
	ReplicationSets []string
	DefaultRepSet   string
	AutoAddTables   bool
	NodeOffset      int  // 1 for primary (odd IDs), 2 for standby (even IDs)
	MaxWaitAttempts int  // Maximum attempts when waiting for remote (default: 30)
	BaseWaitDelayMs int  // Base delay in milliseconds for backoff (default: 100)
	Verbose         bool // Enable verbose logging
}

// TransformedStatement represents a SQL statement after Spock transformation
type TransformedStatement struct {
	Original        string
	Primary         string
	IsDDL           bool
	CreatesTable    bool
	TableInfo       *TableInfo
	SequenceAlters  []string // ALTER SEQUENCE statements for bi-directional replication
}
