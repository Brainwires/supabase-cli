package exec

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/go-errors/errors"
	"github.com/jackc/pgconn"
	"github.com/jackc/pgx/v4"
	"github.com/spf13/afero"
	"github.com/supabase/cli/internal/utils"
	"github.com/supabase/cli/pkg/parser"
	"github.com/supabase/cli/pkg/spock"
)

func Run(ctx context.Context, sql string, file string, spockRemoteDSN string, config pgconn.Config, fsys afero.Fs, options ...func(*pgx.ConnConfig)) error {
	// Get SQL from file, stdin, or argument
	var statements []string
	var err error

	if file == "-" {
		// Read from stdin
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return errors.Errorf("failed to read from stdin: %w", err)
		}
		sql = string(data)
	} else if file != "" {
		// Read from file
		data, err := afero.ReadFile(fsys, file)
		if err != nil {
			return errors.Errorf("failed to read file %s: %w", file, err)
		}
		sql = string(data)
	}

	if sql == "" {
		return errors.New("no SQL provided. Use --sql, --file, or pipe to stdin")
	}

	// Parse SQL into statements
	statements, err = parser.SplitAndTrim(strings.NewReader(sql))
	if err != nil {
		return errors.Errorf("failed to parse SQL: %w", err)
	}

	if len(statements) == 0 {
		fmt.Fprintln(os.Stderr, "No SQL statements to execute.")
		return nil
	}

	// Connect to primary database
	conn, err := utils.ConnectByConfig(ctx, config, options...)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())

	// Check if Spock is enabled on the database
	spockEnabled, err := spock.IsSpockEnabled(ctx, conn)
	if err != nil {
		// If we can't check, assume not enabled
		spockEnabled = false
	}

	if spockEnabled {
		if spockRemoteDSN == "" {
			return errors.New("Spock replication is enabled on this database. Use --spock-remote-dsn to specify the remote node, or connect to a non-Spock database")
		}
		return execWithSpock(ctx, statements, spockRemoteDSN, conn, options...)
	}

	// Execute without Spock
	return execStatements(ctx, statements, conn)
}

func execWithSpock(ctx context.Context, statements []string, spockRemoteDSN string, conn *pgx.Conn, options ...func(*pgx.ConnConfig)) error {
	if spockRemoteDSN == "" {
		return errors.New("Spock enabled but --spock-remote-dsn not provided")
	}

	// Separate queries from other statements
	var queries []string
	var nonQueries []string
	for _, stmt := range statements {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		upperStmt := strings.ToUpper(stmt)
		if strings.HasPrefix(upperStmt, "SELECT") ||
			strings.HasPrefix(upperStmt, "WITH") ||
			strings.HasPrefix(upperStmt, "TABLE") ||
			strings.HasPrefix(upperStmt, "VALUES") {
			queries = append(queries, stmt)
		} else {
			nonQueries = append(nonQueries, stmt)
		}
	}

	// Execute queries without Spock (they're just reads)
	for _, query := range queries {
		if err := execQuery(ctx, query, conn); err != nil {
			return err
		}
	}

	// If no non-query statements, we're done
	if len(nonQueries) == 0 {
		return nil
	}

	fmt.Fprintln(os.Stderr, "Spock mode enabled - connecting to remote node...")
	remoteConn, err := utils.ConnectByUrl(ctx, spockRemoteDSN, options...)
	if err != nil {
		return errors.Errorf("failed to connect to remote Spock node: %w", err)
	}
	defer remoteConn.Close(context.Background())

	// Create Spock executor with defaults (config.toml values used if available)
	spockConfig := spock.Config{
		Enabled:         true,
		ReplicationSets: []string{"default", "ddl_sql"},
		DefaultRepSet:   "default",
		AutoAddTables:   true,
		NodeOffset:      1, // Default to primary
		MaxWaitAttempts: 30,
		BaseWaitDelayMs: 100,
		Verbose:         false,
	}

	// Override with config.toml values if available
	if len(utils.Config.Db.Spock.ReplicationSets) > 0 {
		spockConfig.ReplicationSets = utils.Config.Db.Spock.ReplicationSets
	}
	if utils.Config.Db.Spock.DefaultRepSet != "" {
		spockConfig.DefaultRepSet = utils.Config.Db.Spock.DefaultRepSet
	}
	if utils.Config.Db.Spock.NodeOffset > 0 {
		spockConfig.NodeOffset = utils.Config.Db.Spock.NodeOffset
	}
	if utils.Config.Db.Spock.MaxWaitAttempts > 0 {
		spockConfig.MaxWaitAttempts = utils.Config.Db.Spock.MaxWaitAttempts
	}
	if utils.Config.Db.Spock.BaseWaitDelayMs > 0 {
		spockConfig.BaseWaitDelayMs = utils.Config.Db.Spock.BaseWaitDelayMs
	}
	spockConfig.AutoAddTables = utils.Config.Db.Spock.AutoAddTables
	spockConfig.Verbose = utils.Config.Db.Spock.Verbose

	executor := spock.NewExecutor(conn, remoteConn, spockConfig)

	// Transform statements (wraps DDL in spock.replicate_ddl)
	transformed, err := executor.TransformStatements(nonQueries)
	if err != nil {
		return errors.Errorf("failed to transform statements: %w", err)
	}

	// Execute with replication
	if err := executor.ExecuteWithReplication(ctx, transformed); err != nil {
		return err
	}

	// Print results summary
	ddlCount := 0
	dmlCount := 0
	for _, t := range transformed {
		if t.IsDDL {
			ddlCount++
		} else {
			dmlCount++
		}
	}

	if ddlCount > 0 {
		fmt.Fprintf(os.Stderr, "Executed %d DDL statement(s) with Spock replication\n", ddlCount)
	}
	if dmlCount > 0 {
		fmt.Fprintf(os.Stderr, "Executed %d DML/other statement(s)\n", dmlCount)
	}

	return nil
}

func execStatements(ctx context.Context, statements []string, conn *pgx.Conn) error {
	for i, stmt := range statements {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}

		// Check if it's a SELECT query
		upperStmt := strings.ToUpper(strings.TrimSpace(stmt))
		if strings.HasPrefix(upperStmt, "SELECT") ||
			strings.HasPrefix(upperStmt, "WITH") ||
			strings.HasPrefix(upperStmt, "TABLE") ||
			strings.HasPrefix(upperStmt, "VALUES") {
			if err := execQuery(ctx, stmt, conn); err != nil {
				return errors.Errorf("failed to execute query %d: %w", i+1, err)
			}
			continue
		}

		// Execute non-query statement
		result, err := conn.Exec(ctx, stmt)
		if err != nil {
			return errors.Errorf("failed to execute statement %d: %w\nSQL: %s", i+1, err, truncateSQL(stmt))
		}

		// Print result
		rowsAffected := result.RowsAffected()
		if rowsAffected > 0 {
			fmt.Fprintf(os.Stderr, "Statement %d: %d row(s) affected\n", i+1, rowsAffected)
		} else {
			fmt.Fprintf(os.Stderr, "Statement %d: OK\n", i+1)
		}
	}

	return nil
}

func execQuery(ctx context.Context, sql string, conn *pgx.Conn) error {
	rows, err := conn.Query(ctx, sql)
	if err != nil {
		return errors.Errorf("failed to execute query: %w", err)
	}
	defer rows.Close()

	// Get column names
	fields := rows.FieldDescriptions()
	colNames := make([]string, len(fields))
	colWidths := make([]int, len(fields))
	for i, f := range fields {
		colNames[i] = string(f.Name)
		colWidths[i] = len(colNames[i])
	}

	// Collect all rows first to calculate column widths
	var allRows [][]string
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return errors.Errorf("failed to read row: %w", err)
		}

		strValues := make([]string, len(values))
		for i, v := range values {
			if v == nil {
				strValues[i] = "NULL"
			} else {
				strValues[i] = fmt.Sprintf("%v", v)
			}
			if len(strValues[i]) > colWidths[i] {
				colWidths[i] = len(strValues[i])
			}
		}
		allRows = append(allRows, strValues)
	}

	if err := rows.Err(); err != nil {
		return errors.Errorf("error reading rows: %w", err)
	}

	// Print header
	headerParts := make([]string, len(colNames))
	separatorParts := make([]string, len(colNames))
	for i, name := range colNames {
		headerParts[i] = fmt.Sprintf("%-*s", colWidths[i], name)
		separatorParts[i] = strings.Repeat("-", colWidths[i])
	}
	fmt.Println(strings.Join(headerParts, " | "))
	fmt.Println(strings.Join(separatorParts, "-+-"))

	// Print rows
	for _, row := range allRows {
		rowParts := make([]string, len(row))
		for i, val := range row {
			rowParts[i] = fmt.Sprintf("%-*s", colWidths[i], val)
		}
		fmt.Println(strings.Join(rowParts, " | "))
	}

	fmt.Fprintf(os.Stderr, "(%d row(s))\n", len(allRows))
	return nil
}

func truncateSQL(sql string) string {
	sql = strings.TrimSpace(sql)
	if len(sql) > 100 {
		return sql[:100] + "..."
	}
	return sql
}
