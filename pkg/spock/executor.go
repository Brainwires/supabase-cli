package spock

import (
	"context"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/jackc/pgx/v4"
)

// Executor handles Spock-aware SQL execution
type Executor struct {
	primaryConn *pgx.Conn
	remoteConn  *pgx.Conn
	config      Config
	logger      *log.Logger
}

// NewExecutor creates a new Spock executor
func NewExecutor(primary, remote *pgx.Conn, config Config) *Executor {
	// Apply defaults for configurable values
	if config.MaxWaitAttempts <= 0 {
		config.MaxWaitAttempts = 30
	}
	if config.BaseWaitDelayMs <= 0 {
		config.BaseWaitDelayMs = 100
	}
	if config.NodeOffset <= 0 {
		config.NodeOffset = 1
	}

	e := &Executor{
		primaryConn: primary,
		remoteConn:  remote,
		config:      config,
	}

	// Set up logger based on verbose setting
	if config.Verbose {
		e.logger = log.Default()
	} else {
		e.logger = log.New(io.Discard, "", 0)
	}

	return e
}

// TransformStatements transforms SQL statements for Spock replication
func (e *Executor) TransformStatements(statements []string) ([]TransformedStatement, error) {
	result := make([]TransformedStatement, 0, len(statements))

	for _, stmt := range statements {
		transformed := TransformedStatement{
			Original: stmt,
		}

		if IsDDL(stmt) {
			transformed.Primary = WrapDDL(stmt, e.config.ReplicationSets)
			transformed.IsDDL = true

			if tableInfo := ExtractTableInfo(stmt); tableInfo != nil {
				transformed.CreatesTable = true

				// Extract sequences from CREATE TABLE statement
				sequences := ExtractSequences(stmt, tableInfo)
				tableInfo.Sequences = sequences
				transformed.TableInfo = tableInfo

				// Generate ALTER SEQUENCE statements for bi-directional replication
				if len(sequences) > 0 && e.config.NodeOffset > 0 {
					alters := GenerateAllSequenceAlters(tableInfo, e.config.NodeOffset)
					transformed.SequenceAlters = alters
					e.logger.Printf("[spock] Found %d sequence(s) in table %s.%s",
						len(sequences), tableInfo.Schema, tableInfo.Name)
				}
			}
		} else {
			transformed.Primary = stmt
		}

		result = append(result, transformed)
	}

	return result, nil
}

// ExecuteWithReplication executes statements with Spock replication support
func (e *Executor) ExecuteWithReplication(ctx context.Context, statements []TransformedStatement) error {
	for i, stmt := range statements {
		e.logger.Printf("[spock] Executing statement %d: %s", i, truncateSQL(stmt.Original, 100))

		// Execute the (possibly wrapped) statement on primary
		if _, err := e.primaryConn.Exec(ctx, stmt.Primary); err != nil {
			return fmt.Errorf("failed on primary at statement %d (%s): %w\nSQL: %s",
				i, truncateSQL(stmt.Original, 50), err, stmt.Primary)
		}

		// Execute sequence ALTERs if present (for bi-directional replication)
		if len(stmt.SequenceAlters) > 0 {
			if err := e.executeSequenceAlters(ctx, stmt); err != nil {
				return err
			}
		}

		// If this statement creates a table and auto_add_tables is enabled,
		// add the table to the replication set on both nodes
		if stmt.CreatesTable && e.config.AutoAddTables && stmt.TableInfo != nil {
			if err := e.addTableToReplicationSets(ctx, stmt.TableInfo); err != nil {
				return err
			}
		}
	}

	return nil
}

// executeSequenceAlters executes ALTER SEQUENCE statements on both primary and remote
func (e *Executor) executeSequenceAlters(ctx context.Context, stmt TransformedStatement) error {
	for _, alterSQL := range stmt.SequenceAlters {
		e.logger.Printf("[spock] Executing sequence ALTER: %s", alterSQL)

		// Wrap and execute on primary
		wrappedAlter := WrapDDL(alterSQL, e.config.ReplicationSets)
		if _, err := e.primaryConn.Exec(ctx, wrappedAlter); err != nil {
			return fmt.Errorf("failed to alter sequence on primary: %w\nSQL: %s", err, alterSQL)
		}

		// For the remote node, we need to use a different offset (opposite parity)
		// If primary uses 1 (odd), remote should use 2 (even) and vice versa
		if e.remoteConn != nil && stmt.TableInfo != nil {
			remoteOffset := 3 - e.config.NodeOffset // 1->2, 2->1
			for _, seq := range stmt.TableInfo.Sequences {
				remoteAlterSQL := GenerateSequenceAlterSQL(seq, remoteOffset)

				// Wait for the sequence to exist on remote (it's created as part of table DDL replication)
				if err := e.waitForSequenceOnRemote(ctx, seq.Schema, seq.Name); err != nil {
					return fmt.Errorf("sequence %s.%s not replicated to remote: %w",
						seq.Schema, seq.Name, err)
				}

				e.logger.Printf("[spock] Executing sequence ALTER on remote: %s", remoteAlterSQL)
				if _, err := e.remoteConn.Exec(ctx, remoteAlterSQL); err != nil {
					return fmt.Errorf("failed to alter sequence on remote: %w\nSQL: %s", err, remoteAlterSQL)
				}
			}
		}
	}

	return nil
}

// waitForTableOnRemote waits for a table to exist on the remote node
// This is needed because DDL replication is asynchronous
func (e *Executor) waitForTableOnRemote(ctx context.Context, schema, table string) error {
	if e.remoteConn == nil {
		return fmt.Errorf("remote connection not available")
	}

	checkSQL := fmt.Sprintf(
		"SELECT EXISTS(SELECT 1 FROM pg_tables WHERE schemaname = %s AND tablename = %s)",
		quoteLiteral(schema), quoteLiteral(table),
	)

	return e.waitForCondition(ctx, checkSQL, fmt.Sprintf("table %s.%s", schema, table))
}

// waitForSequenceOnRemote waits for a sequence to exist on the remote node
func (e *Executor) waitForSequenceOnRemote(ctx context.Context, schema, sequence string) error {
	if e.remoteConn == nil {
		return fmt.Errorf("remote connection not available")
	}

	checkSQL := fmt.Sprintf(
		"SELECT EXISTS(SELECT 1 FROM pg_sequences WHERE schemaname = %s AND sequencename = %s)",
		quoteLiteral(schema), quoteLiteral(sequence),
	)

	return e.waitForCondition(ctx, checkSQL, fmt.Sprintf("sequence %s.%s", schema, sequence))
}

// waitForCondition waits for a boolean SQL query to return true on the remote node
func (e *Executor) waitForCondition(ctx context.Context, checkSQL, description string) error {
	maxAttempts := e.config.MaxWaitAttempts
	baseDelay := time.Duration(e.config.BaseWaitDelayMs) * time.Millisecond

	e.logger.Printf("[spock] Waiting for %s on remote (max %d attempts)", description, maxAttempts)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		var exists bool
		if err := e.remoteConn.QueryRow(ctx, checkSQL).Scan(&exists); err != nil {
			return fmt.Errorf("failed to check %s existence on remote: %w", description, err)
		}

		if exists {
			e.logger.Printf("[spock] %s found on remote after %d attempt(s)", description, attempt+1)
			return nil
		}

		// Exponential backoff with cap at 2 seconds
		delay := baseDelay * time.Duration(1<<uint(attempt))
		if delay > 2*time.Second {
			delay = 2 * time.Second
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("context cancelled while waiting for %s: %w", description, ctx.Err())
		case <-time.After(delay):
		}
	}

	return fmt.Errorf("timeout waiting for %s to appear on remote after %d attempts. "+
		"Possible causes: DDL replication lag, replication slot issues, or connectivity problems. "+
		"Check: SELECT * FROM pg_stat_replication; and spock.subscription status",
		description, maxAttempts)
}

// addTableToReplicationSets adds a table to replication sets on both primary and remote
func (e *Executor) addTableToReplicationSets(ctx context.Context, tableInfo *TableInfo) error {
	addTableSQL := GenerateAddTableSQL(
		e.config.DefaultRepSet,
		tableInfo.Schema,
		tableInfo.Name,
		false, // syncData=false avoids sync worker getting stuck at 'i' status
	)

	e.logger.Printf("[spock] Adding table %s.%s to replication set '%s'",
		tableInfo.Schema, tableInfo.Name, e.config.DefaultRepSet)

	// Add to primary replication set
	if _, err := e.primaryConn.Exec(ctx, addTableSQL); err != nil {
		return fmt.Errorf("failed to add table %s.%s to primary repset '%s': %w\nSQL: %s",
			tableInfo.Schema, tableInfo.Name, e.config.DefaultRepSet, err, addTableSQL)
	}

	// Add to remote replication set if connected
	if e.remoteConn != nil {
		// Wait for the table to appear on remote (DDL replication is async)
		if err := e.waitForTableOnRemote(ctx, tableInfo.Schema, tableInfo.Name); err != nil {
			return err
		}

		if _, err := e.remoteConn.Exec(ctx, addTableSQL); err != nil {
			return fmt.Errorf("failed to add table %s.%s to remote repset '%s': %w\nSQL: %s",
				tableInfo.Schema, tableInfo.Name, e.config.DefaultRepSet, err, addTableSQL)
		}

		e.logger.Printf("[spock] Table %s.%s added to replication sets on both nodes",
			tableInfo.Schema, tableInfo.Name)
	}

	return nil
}

// truncateSQL truncates a SQL string for display in error messages
func truncateSQL(sql string, maxLen int) string {
	if len(sql) <= maxLen {
		return sql
	}
	return sql[:maxLen] + "..."
}

// IsSpockEnabled checks if Spock extension is installed and has an active node
// This checks the actual database state, not config files
func IsSpockEnabled(ctx context.Context, conn *pgx.Conn) (bool, error) {
	// Check if spock extension exists and local_node is configured
	var nodeExists bool
	err := conn.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_extension WHERE extname = 'spock'
		) AND EXISTS (
			SELECT 1 FROM spock.local_node
		)
	`).Scan(&nodeExists)
	if err != nil {
		// Table might not exist if spock not installed - that's fine, just means no spock
		return false, nil
	}
	return nodeExists, nil
}
