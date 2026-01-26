package spock

import (
	"context"
	"fmt"
	"os"

	"github.com/go-errors/errors"
	"github.com/jackc/pgconn"
	"github.com/jackc/pgx/v4"
	"github.com/supabase/cli/internal/utils"
)

type EnableOptions struct {
	NodeName        string
	NodeOffset      int
	ReplicationSets []string
}

func RunEnable(ctx context.Context, config pgconn.Config, opts EnableOptions, options ...func(*pgx.ConnConfig)) error {
	fmt.Fprintln(os.Stderr, "Connecting to database...")
	conn, err := utils.ConnectByConfig(ctx, config, options...)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())

	// Check if Spock extension is available
	var available bool
	err = conn.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'spock')
	`).Scan(&available)
	if err != nil {
		return errors.Errorf("failed to check extension availability: %w", err)
	}
	if !available {
		return errors.New("Spock extension is not available. Make sure you're using the supabase-postgres-spock image.")
	}

	// Check if already enabled
	var installed bool
	err = conn.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'spock')
	`).Scan(&installed)
	if err != nil {
		return errors.Errorf("failed to check extension status: %w", err)
	}

	if installed {
		// Check if local node exists
		var nodeExists bool
		err = conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM spock.local_node)`).Scan(&nodeExists)
		if err == nil && nodeExists {
			fmt.Fprintln(os.Stderr, utils.Yellow("Spock is already enabled on this database."))
			fmt.Fprintln(os.Stderr, "Use 'supabase spock status' to view current configuration.")
			return nil
		}
	}

	// Begin setup
	fmt.Fprintln(os.Stderr, "Enabling Spock replication...")

	// Create extension
	if !installed {
		fmt.Fprintln(os.Stderr, "Creating Spock extension...")
		_, err = conn.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS spock")
		if err != nil {
			return errors.Errorf("failed to create Spock extension: %w", err)
		}
	}

	// Verify extension
	var version string
	err = conn.QueryRow(ctx, "SELECT spock.spock_version()").Scan(&version)
	if err != nil {
		return errors.Errorf("failed to verify Spock extension: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Spock version: %s\n", version)

	// Create local node
	fmt.Fprintf(os.Stderr, "Creating local node '%s'...\n", opts.NodeName)
	_, err = conn.Exec(ctx, `
		SELECT spock.node_create(
			node_name := $1,
			dsn := 'host=localhost port=5432 dbname=postgres'
		)
	`, opts.NodeName)
	if err != nil {
		// Check if node already exists
		if pgErr, ok := err.(*pgconn.PgError); ok && pgErr.Code == "23505" {
			fmt.Fprintln(os.Stderr, utils.Yellow("Local node already exists"))
		} else {
			return errors.Errorf("failed to create local node: %w", err)
		}
	}

	// Create replication sets
	for _, setName := range opts.ReplicationSets {
		fmt.Fprintf(os.Stderr, "Creating replication set '%s'...\n", setName)
		_, err = conn.Exec(ctx, "SELECT spock.repset_create($1)", setName)
		if err != nil {
			// Ignore if already exists
			if pgErr, ok := err.(*pgconn.PgError); ok && pgErr.Code == "23505" {
				fmt.Fprintf(os.Stderr, utils.Yellow("  Replication set '%s' already exists\n"), setName)
			} else {
				return errors.Errorf("failed to create replication set '%s': %w", setName, err)
			}
		}
	}

	// Set conflict resolution
	fmt.Fprintln(os.Stderr, "Setting conflict resolution to 'last_update_wins'...")
	_, err = conn.Exec(ctx, "ALTER SYSTEM SET spock.conflict_resolution = 'last_update_wins'")
	if err != nil {
		fmt.Fprintln(os.Stderr, utils.Yellow("Warning: Could not set conflict resolution: "+err.Error()))
	}
	conn.Exec(ctx, "SELECT pg_reload_conf()")

	// Configure sequences if node_offset specified
	if opts.NodeOffset > 0 {
		fmt.Fprintf(os.Stderr, "Configuring sequences for node offset %d...\n", opts.NodeOffset)
		err = configureSequences(ctx, conn, opts.NodeOffset)
		if err != nil {
			fmt.Fprintln(os.Stderr, utils.Yellow("Warning: Could not configure sequences: "+err.Error()))
		}
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, utils.Green("Spock replication enabled successfully!"))
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Next steps:")
	fmt.Fprintln(os.Stderr, "  1. Enable Spock on the remote node with a different node name and offset")
	fmt.Fprintln(os.Stderr, "  2. Create subscriptions between nodes to enable bi-directional replication")
	fmt.Fprintln(os.Stderr, "  3. Add tables to replication sets")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Use 'supabase spock status' to view current configuration.")
	return nil
}

func configureSequences(ctx context.Context, conn *pgx.Conn, nodeOffset int) error {
	// Get all sequences in public schema
	rows, err := conn.Query(ctx, `
		SELECT schemaname, sequencename
		FROM pg_sequences
		WHERE schemaname = 'public'
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	var sequences []struct {
		Schema string
		Name   string
	}
	for rows.Next() {
		var s struct {
			Schema string
			Name   string
		}
		if err := rows.Scan(&s.Schema, &s.Name); err != nil {
			continue
		}
		sequences = append(sequences, s)
	}

	// Configure each sequence
	for _, seq := range sequences {
		fullName := fmt.Sprintf("%s.%s", seq.Schema, seq.Name)

		// Set INCREMENT BY 2
		_, err := conn.Exec(ctx, fmt.Sprintf("ALTER SEQUENCE %s INCREMENT BY 2", fullName))
		if err != nil {
			fmt.Fprintf(os.Stderr, utils.Yellow("  Warning: Could not alter sequence %s: %s\n"), fullName, err.Error())
			continue
		}

		// Get associated table name (assuming _id_seq suffix)
		tableName := ""
		if len(seq.Name) > 7 && seq.Name[len(seq.Name)-7:] == "_id_seq" {
			tableName = seq.Name[:len(seq.Name)-7]
		}

		if tableName != "" {
			// Set sequence value based on max ID + offset
			query := fmt.Sprintf(`
				SELECT setval('%s', COALESCE((SELECT MAX(id) FROM %s.%s), 0) + %d)
			`, fullName, seq.Schema, tableName, nodeOffset)
			_, err = conn.Exec(ctx, query)
			if err != nil {
				// Table might not have id column
				_, err = conn.Exec(ctx, fmt.Sprintf("SELECT setval('%s', currval('%s') - (currval('%s') %% 2) + %d)", fullName, fullName, fullName, nodeOffset))
			}
		}
		fmt.Fprintf(os.Stderr, "  Configured sequence %s\n", fullName)
	}

	return nil
}
