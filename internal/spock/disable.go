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

func RunDisable(ctx context.Context, force bool, config pgconn.Config, options ...func(*pgx.ConnConfig)) error {
	fmt.Fprintln(os.Stderr, "Connecting to database...")
	conn, err := utils.ConnectByConfig(ctx, config, options...)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())

	// Check if Spock extension is installed
	var installed bool
	err = conn.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'spock')
	`).Scan(&installed)
	if err != nil {
		return errors.Errorf("failed to check extension status: %w", err)
	}

	if !installed {
		fmt.Fprintln(os.Stderr, utils.Yellow("Spock extension is not installed on this database."))
		return nil
	}

	// Get status first
	status, err := GetSpockStatus(ctx, conn)
	if err != nil {
		return errors.Errorf("failed to get Spock status: %w", err)
	}

	// Check for active subscriptions
	activeSubscriptions := 0
	for _, sub := range status.Subscriptions {
		if sub.Enabled {
			activeSubscriptions++
		}
	}

	if activeSubscriptions > 0 && !force {
		fmt.Fprintln(os.Stderr, utils.Red("Cannot disable Spock: there are active subscriptions."))
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Active subscriptions:")
		for _, sub := range status.Subscriptions {
			if sub.Enabled {
				fmt.Fprintf(os.Stderr, "  - %s\n", sub.SubName)
			}
		}
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Use --force to disable anyway (will drop all subscriptions).")
		return errors.New("active subscriptions exist")
	}

	// Confirm with user
	if !force {
		msg := "Are you sure you want to disable Spock replication? This will remove all replication configuration."
		if shouldDisable, err := utils.NewConsole().PromptYesNo(ctx, msg, false); err != nil {
			return err
		} else if !shouldDisable {
			return errors.New(context.Canceled)
		}
	}

	fmt.Fprintln(os.Stderr, "Disabling Spock replication...")

	// Disable and drop all subscriptions
	if len(status.Subscriptions) > 0 {
		fmt.Fprintln(os.Stderr, "Removing subscriptions...")
		for _, sub := range status.Subscriptions {
			fmt.Fprintf(os.Stderr, "  Disabling subscription '%s'...\n", sub.SubName)
			_, err = conn.Exec(ctx, "SELECT spock.sub_disable($1, true)", sub.SubName)
			if err != nil {
				fmt.Fprintf(os.Stderr, utils.Yellow("  Warning: Could not disable subscription: %s\n"), err.Error())
			}

			fmt.Fprintf(os.Stderr, "  Dropping subscription '%s'...\n", sub.SubName)
			_, err = conn.Exec(ctx, "SELECT spock.sub_drop($1)", sub.SubName)
			if err != nil {
				fmt.Fprintf(os.Stderr, utils.Yellow("  Warning: Could not drop subscription: %s\n"), err.Error())
			}
		}
	}

	// Drop replication sets (except default ones)
	if len(status.ReplicationSets) > 0 {
		fmt.Fprintln(os.Stderr, "Removing replication sets...")
		for _, rs := range status.ReplicationSets {
			// Skip dropping default sets
			if rs.SetName == "default" || rs.SetName == "default_insert_only" || rs.SetName == "ddl_sql" {
				continue
			}
			fmt.Fprintf(os.Stderr, "  Dropping replication set '%s'...\n", rs.SetName)
			_, err = conn.Exec(ctx, "SELECT spock.repset_drop($1)", rs.SetName)
			if err != nil {
				fmt.Fprintf(os.Stderr, utils.Yellow("  Warning: Could not drop replication set: %s\n"), err.Error())
			}
		}
	}

	// Drop local node
	if status.LocalNode != nil {
		fmt.Fprintf(os.Stderr, "Dropping local node '%s'...\n", status.LocalNode.NodeName)
		_, err = conn.Exec(ctx, "SELECT spock.node_drop($1, true)", status.LocalNode.NodeName)
		if err != nil {
			fmt.Fprintf(os.Stderr, utils.Yellow("Warning: Could not drop local node: %s\n"), err.Error())
		}
	}

	// Drop Spock extension
	fmt.Fprintln(os.Stderr, "Dropping Spock extension...")
	_, err = conn.Exec(ctx, "DROP EXTENSION IF EXISTS spock CASCADE")
	if err != nil {
		return errors.Errorf("failed to drop Spock extension: %w", err)
	}

	// Clean up replication slots
	fmt.Fprintln(os.Stderr, "Cleaning up replication slots...")
	for _, slot := range status.Slots {
		fmt.Fprintf(os.Stderr, "  Dropping slot '%s'...\n", slot.SlotName)
		_, err = conn.Exec(ctx, "SELECT pg_drop_replication_slot($1)", slot.SlotName)
		if err != nil {
			fmt.Fprintf(os.Stderr, utils.Yellow("  Warning: Could not drop replication slot: %s\n"), err.Error())
		}
	}

	// Reset conflict resolution setting
	fmt.Fprintln(os.Stderr, "Resetting configuration...")
	conn.Exec(ctx, "ALTER SYSTEM RESET spock.conflict_resolution")
	conn.Exec(ctx, "SELECT pg_reload_conf()")

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, utils.Green("Spock replication disabled successfully!"))
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Note: Sequences may still have INCREMENT BY 2. Reset manually if needed.")
	return nil
}
