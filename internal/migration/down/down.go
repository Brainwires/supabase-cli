package down

import (
	"context"
	"fmt"
	"os"

	"github.com/go-errors/errors"
	"github.com/jackc/pgconn"
	"github.com/jackc/pgx/v4"
	"github.com/spf13/afero"
	"github.com/supabase/cli/internal/migration/apply"
	"github.com/supabase/cli/internal/utils"
	"github.com/supabase/cli/pkg/migration"
	"github.com/supabase/cli/pkg/vault"
)

func Run(ctx context.Context, last uint, spockRemoteDSN string, config pgconn.Config, fsys afero.Fs, options ...func(*pgx.ConnConfig)) error {
	if last == 0 {
		return errors.Errorf("--last must be greater than 0")
	}
	conn, err := utils.ConnectByConfig(ctx, config, options...)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())

	// Connect to Spock remote if enabled
	var remoteConn *pgx.Conn
	if utils.Config.Db.Spock.Enabled {
		if spockRemoteDSN == "" {
			return errors.New("Spock enabled but --spock-remote-dsn not provided")
		}
		fmt.Fprintln(os.Stderr, "Spock mode enabled - connecting to remote node...")
		remoteConn, err = utils.ConnectByUrl(ctx, spockRemoteDSN, options...)
		if err != nil {
			return errors.Errorf("failed to connect to remote Spock node: %w", err)
		}
		defer remoteConn.Close(context.Background())
	}

	remoteMigrations, err := migration.ListRemoteMigrations(ctx, conn)
	if err != nil {
		return err
	}
	total := uint(len(remoteMigrations))
	if total <= last {
		utils.CmdSuggestion = fmt.Sprintf("Try %s if you want to revert all migrations.", utils.Aqua("supabase db reset"))
		return errors.Errorf("--last must be smaller than total applied migrations: %d", total)
	}
	msg := confirmResetAll(remoteMigrations[total-last:])
	if shouldReset, err := utils.NewConsole().PromptYesNo(ctx, msg, false); err != nil {
		return err
	} else if !shouldReset {
		return errors.New(context.Canceled)
	}
	version := remoteMigrations[total-last-1]
	fmt.Fprintln(os.Stderr, "Resetting database to version:", version)
	return ResetAll(ctx, version, conn, fsys, remoteConn)
}

func ResetAll(ctx context.Context, version string, conn *pgx.Conn, fsys afero.Fs, remoteConn *pgx.Conn) error {
	if err := migration.DropUserSchemas(ctx, conn); err != nil {
		return err
	}
	if err := vault.UpsertVaultSecrets(ctx, utils.Config.Db.Vault, conn); err != nil {
		return err
	}
	return apply.MigrateAndSeed(ctx, version, conn, fsys, remoteConn)
}

func confirmResetAll(pending []string) string {
	msg := fmt.Sprintln("Do you want to revert the following migrations?")
	for _, v := range pending {
		msg += fmt.Sprintf(" • %s\n", utils.Bold(v))
	}
	msg += fmt.Sprintf("%s you will lose all data in this database.", utils.Yellow("WARNING:"))
	return msg
}
