package apply

import (
	"context"

	"github.com/jackc/pgx/v4"
	"github.com/spf13/afero"
	"github.com/supabase/cli/internal/migration/list"
	"github.com/supabase/cli/internal/utils"
	"github.com/supabase/cli/pkg/migration"
)

// MigrateAndSeed applies migrations and seeds with optional Spock replication
// remoteConn can be nil for local operations or when Spock is disabled
func MigrateAndSeed(ctx context.Context, version string, conn *pgx.Conn, fsys afero.Fs, remoteConn *pgx.Conn) error {
	if err := applyMigrationFiles(ctx, version, conn, fsys, remoteConn); err != nil {
		return err
	}
	return applySeedFiles(ctx, conn, fsys)
}

func applyMigrationFiles(ctx context.Context, version string, conn *pgx.Conn, fsys afero.Fs, remoteConn *pgx.Conn) error {
	if !utils.Config.Db.Migrations.Enabled {
		return nil
	}
	migrations, err := list.LoadPartialMigrations(version, fsys)
	if err != nil {
		return err
	}

	// Build SpockOptions if enabled and remote connection provided
	var spockOpts migration.SpockOptions
	if utils.Config.Db.Spock.Enabled && remoteConn != nil {
		spockOpts = migration.SpockOptions{
			Enabled:         true,
			RemoteConn:      remoteConn,
			ReplicationSets: utils.Config.Db.Spock.ReplicationSets,
			DefaultRepSet:   utils.Config.Db.Spock.DefaultRepSet,
			AutoAddTables:   utils.Config.Db.Spock.AutoAddTables,
			NodeOffset:      utils.Config.Db.Spock.NodeOffset,
			MaxWaitAttempts: utils.Config.Db.Spock.MaxWaitAttempts,
			BaseWaitDelayMs: utils.Config.Db.Spock.BaseWaitDelayMs,
			Verbose:         utils.Config.Db.Spock.Verbose,
		}
	}

	return migration.ApplyMigrations(ctx, migrations, conn, afero.NewIOFS(fsys), spockOpts)
}

func applySeedFiles(ctx context.Context, conn *pgx.Conn, fsys afero.Fs) error {
	if !utils.Config.Db.Seed.Enabled {
		return nil
	}
	seeds, err := migration.GetPendingSeeds(ctx, utils.Config.Db.Seed.SqlPaths, conn, afero.NewIOFS(fsys))
	if err != nil {
		return err
	}
	return migration.SeedData(ctx, seeds, conn, afero.NewIOFS(fsys))
}
