package migration

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/go-errors/errors"
	"github.com/jackc/pgx/v4"
	"github.com/supabase/cli/pkg/spock"
)

var (
	ErrMissingRemote = errors.New("Found local migration files to be inserted before the last migration on remote database.")
	ErrMissingLocal  = errors.New("Remote migration versions not found in local migrations directory.")
)

// SpockOptions configures Spock replication for migrations
type SpockOptions struct {
	Enabled         bool
	RemoteConn      *pgx.Conn
	ReplicationSets []string
	DefaultRepSet   string
	AutoAddTables   bool
	NodeOffset      int  // 1 for primary (odd IDs), 2 for standby (even IDs)
	MaxWaitAttempts int  // Max attempts when waiting for remote
	BaseWaitDelayMs int  // Base delay in ms for backoff
	Verbose         bool // Enable verbose logging
}

// Find unapplied local migrations older than the latest migration on
// remote, and remote migrations that are missing from local.
func FindPendingMigrations(localMigrations, remoteMigrations []string) ([]string, error) {
	var unapplied, missing []string
	i, j := 0, 0
	for i < len(remoteMigrations) && j < len(localMigrations) {
		remote := remoteMigrations[i]
		filename := filepath.Base(localMigrations[j])
		// Check if migration has been applied before, LoadLocalMigrations guarantees a match
		local := migrateFilePattern.FindStringSubmatch(filename)[1]
		if remote == local {
			j++
			i++
		} else if remote < local {
			missing = append(missing, remote)
			i++
		} else {
			// Include out-of-order local migrations
			unapplied = append(unapplied, localMigrations[j])
			j++
		}
	}
	// Ensure all remote versions exist on local
	if j == len(localMigrations) {
		missing = append(missing, remoteMigrations[i:]...)
	}
	if len(missing) > 0 {
		return missing, errors.New(ErrMissingLocal)
	}
	// Enforce migrations are applied in chronological order by default
	if len(unapplied) > 0 {
		return unapplied, errors.New(ErrMissingRemote)
	}
	pending := localMigrations[len(remoteMigrations):]
	return pending, nil
}

func ApplyMigrations(ctx context.Context, pending []string, conn *pgx.Conn, fsys fs.FS, opts ...SpockOptions) error {
	if len(pending) > 0 {
		if err := CreateMigrationTable(ctx, conn); err != nil {
			return err
		}
	}

	// Check if Spock mode is enabled
	var spockOpts *SpockOptions
	if len(opts) > 0 && opts[0].Enabled {
		spockOpts = &opts[0]
	}

	for _, path := range pending {
		filename := filepath.Base(path)
		fmt.Fprintf(os.Stderr, "Applying migration %s...\n", filename)
		// Reset all connection settings that might have been modified by another statement on the same connection
		// eg: `SELECT pg_catalog.set_config('search_path', '', false);`
		if _, err := conn.Exec(ctx, "RESET ALL"); err != nil {
			return errors.Errorf("failed to reset connection state: %v", err)
		}

		migration, err := NewMigrationFromFile(path, fsys)
		if err != nil {
			return err
		}

		if spockOpts != nil {
			spockCfg := spock.Config{
				Enabled:         true,
				ReplicationSets: spockOpts.ReplicationSets,
				DefaultRepSet:   spockOpts.DefaultRepSet,
				AutoAddTables:   spockOpts.AutoAddTables,
				NodeOffset:      spockOpts.NodeOffset,
				MaxWaitAttempts: spockOpts.MaxWaitAttempts,
				BaseWaitDelayMs: spockOpts.BaseWaitDelayMs,
				Verbose:         spockOpts.Verbose,
			}
			if err := migration.ExecBatchWithSpock(ctx, conn, spockOpts.RemoteConn, spockCfg); err != nil {
				return err
			}
		} else {
			if err := migration.ExecBatch(ctx, conn); err != nil {
				return err
			}
		}
	}
	return nil
}
