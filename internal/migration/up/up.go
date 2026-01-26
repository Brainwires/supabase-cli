package up

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/go-errors/errors"
	"github.com/jackc/pgconn"
	"github.com/jackc/pgx/v4"
	"github.com/spf13/afero"
	"github.com/supabase/cli/internal/utils"
	"github.com/supabase/cli/pkg/migration"
	"github.com/supabase/cli/pkg/spock"
	"github.com/supabase/cli/pkg/vault"
)

func Run(ctx context.Context, includeAll bool, spockRemoteDSN string, config pgconn.Config, fsys afero.Fs, options ...func(*pgx.ConnConfig)) error {
	conn, err := utils.ConnectByConfig(ctx, config, options...)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())

	// Check if Spock is enabled on the database
	spockEnabled, _ := spock.IsSpockEnabled(ctx, conn)

	// Connect to remote if Spock is enabled
	var remoteConn *pgx.Conn
	if spockEnabled {
		if spockRemoteDSN == "" {
			return errors.New("Spock replication is enabled on this database. Use --spock-remote-dsn to specify the remote node")
		}
		fmt.Fprintln(os.Stderr, "Spock mode enabled - connecting to remote node...")
		remoteConn, err = utils.ConnectByUrl(ctx, spockRemoteDSN, options...)
		if err != nil {
			return errors.Errorf("failed to connect to remote Spock node: %w", err)
		}
		defer remoteConn.Close(context.Background())
	}

	pending, err := GetPendingMigrations(ctx, includeAll, conn, fsys)
	if err != nil {
		return err
	}
	if err := vault.UpsertVaultSecrets(ctx, utils.Config.Db.Vault, conn); err != nil {
		return err
	}

	// Build SpockOptions if Spock is enabled on database
	spockOpts := migration.SpockOptions{}
	if spockEnabled {
		spockOpts = migration.SpockOptions{
			Enabled:         true,
			RemoteConn:      remoteConn,
			ReplicationSets: []string{"default", "ddl_sql"},
			DefaultRepSet:   "default",
			AutoAddTables:   true,
			NodeOffset:      1,
			MaxWaitAttempts: 30,
			BaseWaitDelayMs: 100,
			Verbose:         false,
		}
		// Override with config.toml values if available
		if len(utils.Config.Db.Spock.ReplicationSets) > 0 {
			spockOpts.ReplicationSets = utils.Config.Db.Spock.ReplicationSets
		}
		if utils.Config.Db.Spock.DefaultRepSet != "" {
			spockOpts.DefaultRepSet = utils.Config.Db.Spock.DefaultRepSet
		}
		if utils.Config.Db.Spock.NodeOffset > 0 {
			spockOpts.NodeOffset = utils.Config.Db.Spock.NodeOffset
		}
		if utils.Config.Db.Spock.MaxWaitAttempts > 0 {
			spockOpts.MaxWaitAttempts = utils.Config.Db.Spock.MaxWaitAttempts
		}
		if utils.Config.Db.Spock.BaseWaitDelayMs > 0 {
			spockOpts.BaseWaitDelayMs = utils.Config.Db.Spock.BaseWaitDelayMs
		}
		spockOpts.AutoAddTables = utils.Config.Db.Spock.AutoAddTables
		spockOpts.Verbose = utils.Config.Db.Spock.Verbose
	}

	return migration.ApplyMigrations(ctx, pending, conn, afero.NewIOFS(fsys), spockOpts)
}

func GetPendingMigrations(ctx context.Context, includeAll bool, conn *pgx.Conn, fsys afero.Fs) ([]string, error) {
	remoteMigrations, err := migration.ListRemoteMigrations(ctx, conn)
	if err != nil {
		return nil, err
	}
	localMigrations, err := migration.ListLocalMigrations(utils.MigrationsDir, afero.NewIOFS(fsys))
	if err != nil {
		return nil, err
	}
	diff, err := migration.FindPendingMigrations(localMigrations, remoteMigrations)
	if errors.Is(err, migration.ErrMissingLocal) {
		utils.CmdSuggestion = suggestRevertHistory(diff)
	} else if errors.Is(err, migration.ErrMissingRemote) {
		if includeAll {
			pending := localMigrations[len(remoteMigrations)+len(diff):]
			return append(diff, pending...), nil
		}
		utils.CmdSuggestion = suggestIgnoreFlag(diff)
	}
	return diff, err
}

func suggestRevertHistory(versions []string) string {
	result := fmt.Sprintln("\nMake sure your local git repo is up-to-date. If the error persists, try repairing the migration history table:")
	result += fmt.Sprintln(utils.Bold("supabase migration repair --status reverted " + strings.Join(versions, " ")))
	result += fmt.Sprintln("\nAnd update local migrations to match remote database:")
	result += fmt.Sprintln(utils.Bold("supabase db pull"))
	return result
}

func suggestIgnoreFlag(paths []string) string {
	result := "\nRerun the command with --include-all flag to apply these migrations:\n"
	result += fmt.Sprintln(utils.Bold(strings.Join(paths, "\n")))
	return result
}
